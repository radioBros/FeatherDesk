// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"time"

	"github.com/radioBros/FeatherDesk/internal/store"
)

type kbCatDef struct {
	slug, parent, vis string
	titles            map[string]string
}

var kbCatDefs = []kbCatDef{
	{"getting-started", "", "public", map[string]string{"en": "Getting started", "it": "Primi passi", "de": "Erste Schritte", "fr": "Premiers pas", "es": "Primeros pasos"}},
	{"portal-basics", "getting-started", "public", map[string]string{"en": "Using the portal", "it": "Usare il portale", "de": "Das Portal nutzen", "fr": "Utiliser le portail", "es": "Usar el portal"}},
	{"account", "", "public", map[string]string{"en": "Your account", "it": "Il tuo account", "de": "Ihr Konto", "fr": "Votre compte", "es": "Su cuenta"}},
	{"billing-help", "", "customers", map[string]string{"en": "Billing and invoices", "it": "Fatturazione", "de": "Rechnungen", "fr": "Facturation", "es": "Facturación"}},
	{"troubleshooting", "", "public", map[string]string{"en": "Troubleshooting", "it": "Risoluzione dei problemi", "de": "Fehlerbehebung", "fr": "Dépannage", "es": "Solución de problemas"}},
	{"network", "troubleshooting", "public", map[string]string{"en": "Network and VPN", "it": "Rete e VPN", "de": "Netzwerk und VPN", "fr": "Réseau et VPN", "es": "Red y VPN"}},
	{"agent-handbook", "", "internal", map[string]string{"en": "Agent handbook", "it": "Manuale per gli agenti", "de": "Handbuch für Agenten", "fr": "Manuel des agents", "es": "Manual de agentes"}},
	{"playbooks", "agent-handbook", "internal", map[string]string{"en": "Playbooks", "it": "Procedure", "de": "Playbooks", "fr": "Procédures", "es": "Procedimientos"}},
}

type kbArt struct {
	slug, cat, vis, status string
	tr                     [][3]string // locale, title, body
	views, yes, no         int
}

var kbArts = []kbArt{
	{"getting-started-with-the-portal", "portal-basics", "public", "published", [][3]string{
		{"en", "Getting started with the portal", "Sign in with your email address and password. The **home page** lists your open requests and suggests articles while you type a new one.\n\nUse **New ticket** for anything you cannot solve with the articles here."},
		{"it", "Primi passi con il portale", "Accedi con indirizzo email e password. La **home** mostra le tue richieste aperte e suggerisce articoli mentre scrivi."},
		{"de", "Erste Schritte mit dem Portal", "Melden Sie sich mit E-Mail-Adresse und Passwort an. Auf der **Startseite** sehen Sie Ihre offenen Anfragen."},
		{"fr", "Premiers pas avec le portail", "Connectez-vous avec votre adresse e-mail et votre mot de passe. La **page d'accueil** liste vos demandes ouvertes."},
		{"es", "Primeros pasos con el portal", "Inicie sesión con su correo y contraseña. La **página de inicio** muestra sus solicitudes abiertas."}}, 1240, 86, 9},
	{"open-your-first-ticket", "getting-started", "public", "published", [][3]string{
		{"en", "Open your first ticket", "Choose **New ticket**, pick the category that fits best, describe the problem and attach screenshots. You get an email for every reply."},
		{"it", "Apri il tuo primo ticket", "Scegli **Nuovo ticket**, seleziona la categoria, descrivi il problema e allega gli screenshot. Riceverai un'email per ogni risposta."},
		{"de", "Ihr erstes Ticket erstellen", "Wählen Sie **Neues Ticket**, die passende Kategorie, beschreiben Sie das Problem und hängen Sie Screenshots an."},
		{"fr", "Ouvrir votre premier ticket", "Choisissez **Nouveau ticket**, la catégorie, décrivez le problème et joignez des captures d'écran."},
		{"es", "Abrir su primer ticket", "Elija **Nuevo ticket**, la categoría, describa el problema y adjunte capturas de pantalla."}}, 2310, 174, 12},
	{"track-ticket-status", "getting-started", "public", "published", [][3]string{
		{"en", "Track the status of a ticket", "Every ticket is *open*, *awaiting your reply*, *resolved* or *closed*. The list in the portal always shows the current status."},
		{"it", "Segui lo stato di un ticket", "Ogni ticket è *aperto*, *in attesa di risposta*, *risolto* o *chiuso*."}}, 870, 51, 4},
	{"reply-by-email", "getting-started", "public", "published", [][3]string{
		{"en", "Reply by email", "Answer the notification email directly: your reply is added to the ticket thread. Do not change the subject line."},
		{"it", "Rispondi via email", "Rispondi direttamente all'email di notifica: la risposta viene aggiunta al ticket."}}, 640, 38, 7},
	{"attach-files-to-a-ticket", "portal-basics", "public", "published", [][3]string{
		{"en", "Attach files to a ticket", "Drag files onto the reply box. Images, PDFs and logs up to the configured size are accepted."},
		{"it", "Allega file a un ticket", "Trascina i file nella casella di risposta. Sono accettati immagini, PDF e log."},
		{"de", "Dateien an ein Ticket anhängen", "Ziehen Sie Dateien in das Antwortfeld. Bilder, PDFs und Logs werden akzeptiert."}}, 455, 29, 3},
	{"reset-your-password", "account", "public", "published", [][3]string{
		{"en", "Reset your password", "Choose **Forgot password** on the sign-in page and follow the link in the email. The link expires after one hour."},
		{"it", "Reimposta la password", "Scegli **Password dimenticata** nella pagina di accesso e segui il link nell'email. Il link scade dopo un'ora."},
		{"de", "Passwort zurücksetzen", "Wählen Sie **Passwort vergessen** und folgen Sie dem Link in der E-Mail. Der Link läuft nach einer Stunde ab."},
		{"fr", "Réinitialiser votre mot de passe", "Choisissez **Mot de passe oublié** et suivez le lien reçu par e-mail. Le lien expire après une heure."},
		{"es", "Restablecer su contraseña", "Elija **¿Olvidó su contraseña?** y siga el enlace del correo. El enlace caduca en una hora."}}, 3120, 301, 22},
	{"two-factor-and-sso", "account", "public", "draft", [][3]string{
		{"en", "Two-factor authentication and single sign-on", "*Draft:* explain how to enable your identity provider and what changes for existing accounts."}}, 0, 0, 0},
	{"personal-api-tokens", "account", "customers", "published", [][3]string{
		{"en", "Personal API tokens", "Create a token with the `tickets:read` or `tickets:write` scope in your profile to automate support requests. Treat it like a password."},
		{"it", "Token API personali", "Crea un token con lo scope `tickets:read` o `tickets:write` nel tuo profilo per automatizzare le richieste."}}, 210, 17, 2},
	{"download-your-invoices", "billing-help", "customers", "published", [][3]string{
		{"en", "Download your invoices", "Invoices are emailed on the first working day of each month. Need an older copy? Open a ticket in **Billing** with the invoice number."},
		{"it", "Scarica le tue fatture", "Le fatture arrivano via email il primo giorno lavorativo del mese. Per una copia più vecchia apri un ticket in **Fatturazione**."},
		{"de", "Rechnungen herunterladen", "Rechnungen werden am ersten Arbeitstag des Monats per E-Mail versandt."},
		{"fr", "Télécharger vos factures", "Les factures sont envoyées par e-mail le premier jour ouvré de chaque mois."}}, 980, 88, 6},
	{"payment-methods-and-refunds", "billing-help", "customers", "published", [][3]string{
		{"en", "Payment methods and refunds", "We accept bank transfer and card. Refunds go back to the original payment method within 5-7 business days."},
		{"it", "Metodi di pagamento e rimborsi", "Accettiamo bonifico e carta. I rimborsi tornano sul metodo originale entro 5-7 giorni lavorativi."}}, 530, 41, 8},
	{"vat-and-invoice-details", "billing-help", "customers", "archived", [][3]string{
		{"en", "VAT and invoice details (old process)", "*Archived:* the VAT form was replaced by the customer profile fields."},
		{"it", "IVA e dati di fatturazione (vecchio processo)", "*Archiviato:* il modulo IVA è stato sostituito dai campi del profilo."}}, 140, 6, 11},
	{"vpn-keeps-disconnecting", "network", "public", "published", [][3]string{
		{"en", "The VPN keeps disconnecting", "1. Update the VPN client to the latest version.\n2. Switch from Wi-Fi to a cable to rule out signal loss.\n3. Disable *power saving* for the network adapter.\n\nStill failing? Open a ticket and attach the client log."},
		{"it", "La VPN si disconnette continuamente", "Aggiorna il client VPN, prova con un cavo di rete e disattiva il risparmio energetico della scheda di rete."},
		{"de", "Die VPN-Verbindung bricht ab", "Aktualisieren Sie den VPN-Client, testen Sie ein Netzwerkkabel und deaktivieren Sie die Energiesparfunktion der Netzwerkkarte."},
		{"fr", "Le VPN se déconnecte sans cesse", "Mettez à jour le client VPN, essayez un câble réseau et désactivez l'économie d'énergie de la carte réseau."},
		{"es", "La VPN se desconecta constantemente", "Actualice el cliente VPN, pruebe con un cable de red y desactive el ahorro de energía del adaptador."}}, 2780, 240, 31},
	{"wifi-troubleshooting", "network", "public", "published", [][3]string{
		{"en", "Wi-Fi troubleshooting", "Forget the network and join again, move closer to the access point and check that the date and time are correct."},
		{"it", "Problemi con il Wi-Fi", "Dimentica la rete e riconnettiti, avvicinati al punto di accesso e verifica data e ora."}}, 1190, 97, 14},
	{"printer-offline-checklist", "troubleshooting", "public", "draft", [][3]string{
		{"en", "Printer offline: checklist", "*Draft:* power cycle, check the queue, reinstall the driver."}}, 0, 0, 0},
	{"old-browser-support-policy", "troubleshooting", "public", "archived", [][3]string{
		{"en", "Supported browsers (before 2026)", "*Archived:* Internet Explorer is no longer supported. See the current list of browsers."},
		{"it", "Browser supportati (prima del 2026)", "*Archiviato:* Internet Explorer non è più supportato."}}, 75, 2, 5},
	{"escalation-playbook", "playbooks", "internal", "published", [][3]string{
		{"en", "Escalation playbook", "Escalate from **L1 to L2** when the fix needs configuration access, and to **L3** for suspected product bugs. Always write the reason: *what you tried*, *what you saw*, *what you need*."},
		{"it", "Procedura di escalation", "Escala da **L1 a L2** quando serve accesso alla configurazione e a **L3** per sospetti bug. Scrivi sempre il motivo."}}, 310, 24, 1},
	{"sla-and-priorities-cheat-sheet", "playbooks", "internal", "published", [][3]string{
		{"en", "SLA and priorities cheat sheet", "| Priority | First response | Resolution |\n|---|---|---|\n| Urgent | 30 min | 4 h |\n| High | 2 h | 1 day |\n| Normal | 8 h | 2 days |\n| Low | 1 day | 5 days |"}}, 405, 33, 0},
	{"customer-data-requests", "agent-handbook", "internal", "published", [][3]string{
		{"en", "Handling customer data requests (GDPR)", "Never export data on request of a ticket alone. Ask a supervisor to verify the identity first and log the decision in an internal note."},
		{"de", "Umgang mit Datenauskünften (DSGVO)", "Exportieren Sie niemals Daten allein aufgrund eines Tickets. Lassen Sie die Identität von einem Supervisor prüfen."}}, 120, 9, 0},
}

func (s *seeder) setupKB() error {
	a := s.staff["sup"].cl
	s.sim(s.now.AddDate(0, 0, -58))
	catID := map[string]string{}
	for i, c := range kbCatDefs {
		body := map[string]any{"slug": c.slug, "title": c.titles, "visibility": c.vis, "sort": i}
		if c.parent != "" {
			body["parent_id"] = catID[c.parent]
		}
		var out idResp
		if err := s.admin.post("/admin/kb/categories", body, &out); err != nil {
			return fmt.Errorf("kb category %s: %w", c.slug, err)
		}
		catID[c.slug] = out.ID
	}
	// D-20: a Mobile KB category (shown with the Mobile portal entry /p/MOB).
	if err := s.admin.post("/admin/kb/categories", map[string]any{"slug": "mobile-app", "visibility": "public", "sort": len(kbCatDefs),
		"title": map[string]string{"en": "Mobile app", "it": "App mobile"}, "project_id": s.projects["mob"].ID}, nil); err != nil {
		return fmt.Errorf("kb category mobile-app: %w", err)
	}
	for _, art := range kbArts {
		first := art.tr[0]
		var out idResp
		if err := a.post("/agent/kb/articles", map[string]any{"category_id": catID[art.cat], "locale": first[0], "title": first[1], "body_md": first[2],
			"slug": art.slug, "visibility": art.vis}, &out); err != nil {
			return fmt.Errorf("kb article %s: %w", art.slug, err)
		}
		for _, t := range art.tr[1:] {
			if err := a.put("/agent/kb/articles/"+out.ID+"/translations/"+t[0], map[string]string{"title": t[1], "body_md": t[2]}, nil); err != nil {
				return fmt.Errorf("kb translation %s/%s: %w", art.slug, t[0], err)
			}
		}
		if art.status != "draft" {
			if err := a.patch("/agent/kb/articles/"+out.ID, map[string]any{"status": "published"}, nil); err != nil {
				return fmt.Errorf("kb publish %s: %w", art.slug, err)
			}
			if art.status == "archived" {
				s.advance(24 * time.Hour)
				if err := a.patch("/agent/kb/articles/"+out.ID, map[string]any{"status": "archived"}, nil); err != nil {
					return fmt.Errorf("kb archive %s: %w", art.slug, err)
				}
			}
		}
		s.kbArticles[art.slug] = out.ID
		s.advance(36 * time.Hour)
	}
	return nil
}

// kbCounters sets view and "was this helpful" counters. The service keeps
// them in a coalescing buffer that is flushed by the running server, so a
// short-lived seeder writes the totals (and a few vote rows) directly.
func (s *seeder) kbCounters() error {
	for _, art := range kbArts {
		id := s.kbArticles[art.slug]
		if id == "" || art.views == 0 {
			continue
		}
		if err := s.exec(`UPDATE kb_articles SET views = ?, helpful_yes = ?, helpful_no = ? WHERE id = ?`, art.views, art.yes, art.no, id); err != nil {
			return err
		}
		votes := min(art.yes+art.no, 8)
		for i := 0; i < votes; i++ {
			at := s.now.Add(-time.Duration(1+s.rng.IntN(50*24)) * time.Hour)
			if err := s.exec(`INSERT INTO kb_article_feedback (id, article_id, helpful, created_at) VALUES (?, ?, ?, ?)`,
				store.NewID(), id, i < art.yes*votes/(art.yes+art.no), at); err != nil {
				return err
			}
		}
	}
	return nil
}
