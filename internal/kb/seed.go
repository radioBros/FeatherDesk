// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/cli"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// KB-015: demo content (3 categories × 5 articles in EN and IT about the
// product itself) and the 5k-article perf fixture used by the KB-010 bench.

func init() {
	cli.Register(cli.Command{
		Name:    "seed-kb",
		Summary: "Add demo knowledge-base content (3 categories × 5 articles, EN + IT)",
		Usage:   "seed-kb",
		Run: func(ctx context.Context, args []string) (err error) {
			fs := flag.NewFlagSet("seed-kb", flag.ContinueOnError)
			if err := fs.Parse(args); err != nil {
				return err
			}
			cfg, err := config.Load(os.Getenv)
			if err != nil {
				return err
			}
			db, err := store.Open(ctx, cfg.EffectiveDatabaseURL(), store.Options{Readers: cfg.DBReaders, PoolSize: cfg.DBPool})
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, db.Close()) }()
			if err := db.Migrate(ctx); err != nil {
				return err
			}
			n, err := Seed(ctx, db)
			if err != nil {
				return err
			}
			fmt.Printf("seeded %d knowledge-base articles\n", n)
			return nil
		},
	})
}

// ErrAlreadySeeded: the demo categories exist already.
var ErrAlreadySeeded = errors.New("kb: demo content already present")

type seedArticle struct{ slug, en, enBody, it, itBody string }

var seedContent = []struct {
	slug, en, it string
	articles     []seedArticle
}{
	{"getting-started", "Getting started", "Primi passi", []seedArticle{
		{"open-your-first-ticket", "Open your first ticket", "Sign in to the portal, choose **New ticket**, describe the problem and attach screenshots. You get an email for every reply.",
			"Apri il tuo primo ticket", "Accedi al portale, scegli **Nuovo ticket**, descrivi il problema e allega gli screenshot. Riceverai un'email per ogni risposta."},
		{"track-ticket-status", "Track the status of a ticket", "Every ticket is *open*, *awaiting your reply*, *resolved* or *closed*. The portal list shows the current status.",
			"Segui lo stato di un ticket", "Ogni ticket è *aperto*, *in attesa di risposta*, *risolto* o *chiuso*. L'elenco del portale mostra lo stato attuale."},
		{"reply-by-email", "Reply by email", "Answer the notification email directly: your reply is added to the ticket thread.",
			"Rispondi via email", "Rispondi direttamente all'email di notifica: la risposta viene aggiunta al ticket."},
		{"attach-files", "Attach files to a ticket", "Drag files onto the reply box. Images, PDFs and logs up to the configured size are accepted.",
			"Allega file a un ticket", "Trascina i file nella casella di risposta. Sono accettati immagini, PDF e log fino alla dimensione configurata."},
		{"search-the-knowledge-base", "Search the knowledge base", "Type at least three letters in the search box: matching articles appear while you type.",
			"Cerca nella knowledge base", "Digita almeno tre lettere nella casella di ricerca: gli articoli corrispondenti compaiono mentre scrivi."},
	}},
	{"account", "Your account", "Il tuo account", []seedArticle{
		{"reset-your-password", "Reset your password", "Choose **Forgot password** on the sign-in page and follow the link in the email. The link expires after one hour.",
			"Reimposta la password", "Scegli **Password dimenticata** nella pagina di accesso e segui il link nell'email. Il link scade dopo un'ora."},
		{"change-your-language", "Change your language", "Open your profile and pick English, Italiano, Deutsch, Français or Español.",
			"Cambia la lingua", "Apri il tuo profilo e scegli English, Italiano, Deutsch, Français o Español."},
		{"notification-settings", "Notification settings", "Decide which emails you receive from your profile page.",
			"Impostazioni delle notifiche", "Decidi quali email ricevere dalla pagina del profilo."},
		{"api-tokens", "Personal API tokens", "Create a token with the `tickets:read` or `tickets:write` scope to automate your support requests.",
			"Token API personali", "Crea un token con lo scope `tickets:read` o `tickets:write` per automatizzare le tue richieste."},
		{"organization-members", "Manage organization members", "Organization managers can invite colleagues and see every ticket of the organization.",
			"Gestisci i membri dell'organizzazione", "I responsabili dell'organizzazione possono invitare colleghi e vedere tutti i ticket dell'organizzazione."},
	}},
	{"troubleshooting", "Troubleshooting", "Risoluzione dei problemi", []seedArticle{
		{"no-notification-email", "I do not receive notification emails", "Check your spam folder and make sure the sender address is allowed by your mail server.",
			"Non ricevo le email di notifica", "Controlla la cartella spam e verifica che l'indirizzo del mittente sia consentito dal tuo server di posta."},
		{"cannot-sign-in", "I cannot sign in", "Make sure your account is active. After too many attempts, wait a few minutes before trying again.",
			"Non riesco ad accedere", "Verifica che il tuo account sia attivo. Dopo troppi tentativi, attendi qualche minuto prima di riprovare."},
		{"upload-fails", "An upload fails", "Files larger than the configured limit are refused. Compress large logs before attaching them.",
			"Il caricamento non riesce", "I file più grandi del limite configurato vengono rifiutati. Comprimi i log grandi prima di allegarli."},
		{"reopen-a-ticket", "Reopen a resolved ticket", "Reply within seven days of the resolution to reopen the ticket; later replies open a follow-up.",
			"Riapri un ticket risolto", "Rispondi entro sette giorni dalla risoluzione per riaprire il ticket; le risposte successive aprono un nuovo ticket collegato."},
		{"browser-support", "Supported browsers", "Use a current version of Firefox, Chrome, Edge or Safari.",
			"Browser supportati", "Usa una versione recente di Firefox, Chrome, Edge o Safari."},
	}},
}

// Seed adds the demo content (published, public) and returns the number of
// articles created.
func Seed(ctx context.Context, db *store.DB) (int, error) {
	n := 0
	err := db.Tx(ctx, func(tx *store.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_categories WHERE slug = ?`, seedContent[0].slug).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return ErrAlreadySeeded
		}
		now := store.Now()
		for ci, c := range seedContent {
			catID := store.NewID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO kb_categories (id, slug, title, sort, visibility, created_at, updated_at) VALUES (?, ?, ?, ?, 'public', ?, ?)`,
				catID, c.slug, store.NewJSON(map[string]string{"en": c.en, "it": c.it}), ci, now, now); err != nil {
				return err
			}
			for _, a := range c.articles {
				id := store.NewID()
				if _, err := tx.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, status, visibility, published_at, created_at, updated_at) VALUES (?, ?, ?, 'published', 'public', ?, ?, ?)`,
					id, catID, a.slug, now, now, now); err != nil {
					return err
				}
				for _, t := range [][3]string{{"en", a.en, a.enBody}, {"it", a.it, a.itBody}} {
					if _, err := tx.ExecContext(ctx, `INSERT INTO kb_article_translations (article_id, locale, title, body_md, updated_at) VALUES (?, ?, ?, ?, ?)`,
						id, t[0], t[1], t[2], now); err != nil {
						return err
					}
				}
				if err := reindex(ctx, tx, id); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	})
	return n, err
}

// PerfVocabulary is the word list of the perf fixture; queries built from it
// hit a realistic fraction of the articles.
var PerfVocabulary = func() []string {
	// 32 syllables: a 3-letter prefix covers ~3% of the vocabulary, denser
	// than natural language, so broad "while typing" prefixes stay realistic.
	syl := []string{"ka", "lo", "mi", "ne", "pu", "ra", "si", "to", "ve", "zu", "bri", "cor", "dan", "fel", "gor", "hal",
		"jen", "kru", "lam", "mos", "nix", "orb", "pel", "qua", "rin", "sol", "tem", "urb", "vex", "wol", "yar", "zed"}
	out := make([]string, 0, 1500)
	r := rand.New(rand.NewPCG(1, 2))
	seen := map[string]bool{}
	for len(out) < 1500 {
		w := syl[r.IntN(len(syl))] + syl[r.IntN(len(syl))] + syl[r.IntN(len(syl))]
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}()

// SeedPerf builds n articles across 10 categories with one translation per
// locale (KB-010 fixture: 5k articles × 3 locales). Visibility and status are
// mixed so the I-10 filter does real work. Inserts are batched.
func SeedPerf(ctx context.Context, db *store.DB, n int, locales []string) error {
	r := rand.New(rand.NewPCG(7, 11))
	words := func(k int) string {
		ws := make([]string, k)
		for i := range ws {
			ws[i] = PerfVocabulary[r.IntN(len(PerfVocabulary))]
		}
		return strings.Join(ws, " ")
	}
	now := store.Now()
	vis := []string{VisPublic, VisCustomers, VisInternal}
	cats := make([]string, 10)
	catVis := make([]string, 10)
	err := db.Tx(ctx, func(tx *store.Tx) error {
		for i := range cats {
			cats[i] = store.NewID()
			catVis[i] = vis[i%3]
			if _, err := tx.ExecContext(ctx, `INSERT INTO kb_categories (id, slug, title, sort, visibility, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				cats[i], fmt.Sprintf("perf-%d", i), store.NewJSON(map[string]string{"en": "Perf " + fmt.Sprint(i)}), i, catVis[i], now, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	const batch = 250
	for start := 0; start < n; start += batch {
		end := min(n, start+batch)
		err := db.Tx(ctx, func(tx *store.Tx) error {
			var arts, trs []string
			var aArgs, tArgs []any
			var docs []store.KBDoc
			for i := start; i < end; i++ {
				id := store.NewID()
				ci := i % len(cats)
				status := "published"
				if i%10 == 9 {
					status = "draft"
				}
				arts = append(arts, "(?, ?, ?, ?, ?, ?, ?, ?)")
				aArgs = append(aArgs, id, cats[ci], fmt.Sprintf("perf-article-%d", i), status, catVis[ci], now, now, now)
				for _, l := range locales {
					title, body := words(5), words(60)
					trs = append(trs, "(?, ?, ?, ?, ?)")
					tArgs = append(tArgs, id, l, title, body, now)
					docs = append(docs, store.KBDoc{ArticleID: id, Locale: l, Title: title, Body: body})
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, status, visibility, published_at, created_at, updated_at) VALUES `+strings.Join(arts, ", "), aArgs...); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO kb_article_translations (article_id, locale, title, body_md, updated_at) VALUES `+strings.Join(trs, ", "), tArgs...); err != nil {
				return err
			}
			return indexBatch(ctx, tx, docs)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// indexBatch writes many index rows with multi-row inserts (fixture only;
// the service path uses store.IndexKB per translation).
func indexBatch(ctx context.Context, tx *store.Tx, docs []store.KBDoc) error {
	if len(docs) == 0 {
		return nil
	}
	var vals []string
	var args []any
	for _, d := range docs {
		if tx.Dialect() == store.Postgres {
			vals = append(vals, "(?, ?, ?, setweight(to_tsvector('simple', ?), 'A') || setweight(to_tsvector('simple', ?), 'B'))")
			args = append(args, d.ArticleID, d.Locale, d.Title, d.Title, d.Body)
		} else {
			vals = append(vals, "(?, ?, ?, ?)")
			args = append(args, d.ArticleID, d.Locale, d.Title, d.Body)
		}
	}
	q := `INSERT INTO kb_fts (article_id, locale, title, body) VALUES `
	if tx.Dialect() == store.Postgres {
		q = `INSERT INTO kb_search (article_id, locale, title, tsv) VALUES `
	}
	_, err := tx.ExecContext(ctx, q+strings.Join(vals, ", "), args...)
	return err
}
