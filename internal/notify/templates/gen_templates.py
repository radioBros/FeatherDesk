#!/usr/bin/env python3
"""Generates internal/notify/templates/<locale>/*.tmpl from one translation table.
Usage: python3 gen_templates.py <repo root>"""
import os, sys

ROOT = sys.argv[1]
OUT = os.path.join(ROOT, 'internal', 'notify', 'templates')

# Shared snippets (placeholders identical in every locale).
T = {}  # key -> locale -> (subject, body)

def add(key, **locs):
    T[key] = locs

OPEN_C = '[{}]({{{{.Ticket.URL}}}})'
OPEN_S = '[{}]({{{{.Ticket.URL}}}})'

add('customer.ticket_received',
 en=('We received your request: {{.Ticket.Subject}}',
 'Hello {{.RecipientName}},\n\nthank you for contacting us. We have received your request **{{.Ticket.Subject}}** and will get back to you as soon as possible.\n\nYour reference number is **#{{.Ticket.Number}}**.\n\n[View your request]({{.Ticket.URL}})'),
 it=('Abbiamo ricevuto la tua richiesta: {{.Ticket.Subject}}',
 'Ciao {{.RecipientName}},\n\ngrazie per averci contattato. Abbiamo ricevuto la tua richiesta **{{.Ticket.Subject}}** e ti risponderemo il prima possibile.\n\nIl tuo numero di riferimento è **#{{.Ticket.Number}}**.\n\n[Visualizza la richiesta]({{.Ticket.URL}})'),
 de=('Wir haben Ihre Anfrage erhalten: {{.Ticket.Subject}}',
 'Hallo {{.RecipientName}},\n\nvielen Dank für Ihre Nachricht. Wir haben Ihre Anfrage **{{.Ticket.Subject}}** erhalten und melden uns so schnell wie möglich.\n\nIhre Referenznummer lautet **#{{.Ticket.Number}}**.\n\n[Anfrage ansehen]({{.Ticket.URL}})'),
 fr=('Nous avons bien reçu votre demande : {{.Ticket.Subject}}',
 'Bonjour {{.RecipientName}},\n\nmerci de nous avoir contactés. Nous avons bien reçu votre demande **{{.Ticket.Subject}}** et nous vous répondrons dans les meilleurs délais.\n\nVotre numéro de référence est **#{{.Ticket.Number}}**.\n\n[Voir votre demande]({{.Ticket.URL}})'),
 es=('Hemos recibido su solicitud: {{.Ticket.Subject}}',
 'Hola {{.RecipientName}},\n\ngracias por contactarnos. Hemos recibido su solicitud **{{.Ticket.Subject}}** y le responderemos lo antes posible.\n\nSu número de referencia es **#{{.Ticket.Number}}**.\n\n[Ver su solicitud]({{.Ticket.URL}})'),
)

SIG = '{{with .Signature}}\n\n{{.}}{{end}}'
add('customer.reply',
 en=('New reply to your request: {{.Ticket.Subject}}',
 'Hello {{.RecipientName}},\n\n{{.Message.AuthorName}} replied to your request **{{.Ticket.Subject}}**:\n\n{{.Message.Body}}' + SIG + '\n\n[View the conversation]({{.Ticket.URL}})'),
 it=('Nuova risposta alla tua richiesta: {{.Ticket.Subject}}',
 'Ciao {{.RecipientName}},\n\n{{.Message.AuthorName}} ha risposto alla tua richiesta **{{.Ticket.Subject}}**:\n\n{{.Message.Body}}' + SIG + '\n\n[Visualizza la conversazione]({{.Ticket.URL}})'),
 de=('Neue Antwort auf Ihre Anfrage: {{.Ticket.Subject}}',
 'Hallo {{.RecipientName}},\n\n{{.Message.AuthorName}} hat auf Ihre Anfrage **{{.Ticket.Subject}}** geantwortet:\n\n{{.Message.Body}}' + SIG + '\n\n[Unterhaltung ansehen]({{.Ticket.URL}})'),
 fr=('Nouvelle réponse à votre demande : {{.Ticket.Subject}}',
 'Bonjour {{.RecipientName}},\n\n{{.Message.AuthorName}} a répondu à votre demande **{{.Ticket.Subject}}** :\n\n{{.Message.Body}}' + SIG + '\n\n[Voir la conversation]({{.Ticket.URL}})'),
 es=('Nueva respuesta a su solicitud: {{.Ticket.Subject}}',
 'Hola {{.RecipientName}},\n\n{{.Message.AuthorName}} ha respondido a su solicitud **{{.Ticket.Subject}}**:\n\n{{.Message.Body}}' + SIG + '\n\n[Ver la conversación]({{.Ticket.URL}})'),
)

add('customer.resolved',
 en=('Your request has been resolved: {{.Ticket.Subject}}',
 'Hello {{.RecipientName}},\n\nyour request **{{.Ticket.Subject}}** has been marked as resolved. If you still need help, simply reply to this email.{{if .CSATURL}}\n\nHow did we do? [Rate our support]({{.CSATURL}}){{end}}\n\n[View your request]({{.Ticket.URL}})'),
 it=('La tua richiesta è stata risolta: {{.Ticket.Subject}}',
 'Ciao {{.RecipientName}},\n\nla tua richiesta **{{.Ticket.Subject}}** è stata contrassegnata come risolta. Se hai ancora bisogno di aiuto, rispondi semplicemente a questa email.{{if .CSATURL}}\n\nCome siamo andati? [Valuta il nostro supporto]({{.CSATURL}}){{end}}\n\n[Visualizza la richiesta]({{.Ticket.URL}})'),
 de=('Ihre Anfrage wurde gelöst: {{.Ticket.Subject}}',
 'Hallo {{.RecipientName}},\n\nIhre Anfrage **{{.Ticket.Subject}}** wurde als gelöst markiert. Falls Sie weiterhin Hilfe benötigen, antworten Sie einfach auf diese E-Mail.{{if .CSATURL}}\n\nWie zufrieden waren Sie? [Support bewerten]({{.CSATURL}}){{end}}\n\n[Anfrage ansehen]({{.Ticket.URL}})'),
 fr=('Votre demande a été résolue : {{.Ticket.Subject}}',
 'Bonjour {{.RecipientName}},\n\nvotre demande **{{.Ticket.Subject}}** a été marquée comme résolue. Si vous avez encore besoin d\'aide, répondez simplement à cet e-mail.{{if .CSATURL}}\n\nVotre avis compte : [évaluez notre support]({{.CSATURL}}){{end}}\n\n[Voir votre demande]({{.Ticket.URL}})'),
 es=('Su solicitud ha sido resuelta: {{.Ticket.Subject}}',
 'Hola {{.RecipientName}},\n\nsu solicitud **{{.Ticket.Subject}}** se ha marcado como resuelta. Si todavía necesita ayuda, simplemente responda a este correo.{{if .CSATURL}}\n\n¿Qué tal lo hicimos? [Valore nuestro soporte]({{.CSATURL}}){{end}}\n\n[Ver su solicitud]({{.Ticket.URL}})'),
)

MSGS = '{{range .Messages}}\n\n**{{.AuthorName}}:**\n\n{{.Body}}{{end}}'
FIELDS = {
 'en': ('Subject', 'Priority', 'Team', 'Tier'),
 'it': ('Oggetto', 'Priorità', 'Team', 'Livello'),
 'de': ('Betreff', 'Priorität', 'Team', 'Stufe'),
 'fr': ('Objet', 'Priorité', 'Équipe', 'Niveau'),
 'es': ('Asunto', 'Prioridad', 'Equipo', 'Nivel'),
}
def fields(l):
    s, p, t, n = FIELDS[l]
    return (f'\n\n- **{s}:** {{{{.Ticket.Subject}}}}\n- **{p}:** {{{{.Ticket.Priority}}}}\n'
            f'- **{t}:** {{{{.Ticket.TeamName}}}}\n- **{n}:** {{{{.Ticket.TierLabel}}}}')
OPEN = {'en': 'Open the ticket', 'it': 'Apri il ticket', 'de': 'Ticket öffnen', 'fr': 'Ouvrir le ticket', 'es': 'Abrir el ticket'}
def op(l): return f'\n\n[{OPEN[l]}]({{{{.Ticket.URL}}}})'
HELLO = {'en': 'Hello', 'it': 'Ciao', 'de': 'Hallo', 'fr': 'Bonjour', 'es': 'Hola'}
def hi(l): return HELLO[l] + ' {{.RecipientName}},\n\n'
REF = '**#{{.Ticket.Number}} {{.Ticket.Subject}}**'

add('staff.ticket_new',
 en=('New ticket: {{.Ticket.Subject}}', hi('en') + 'a new ticket was opened by {{.Ticket.RequesterName}}.' + fields('en') + MSGS + op('en')),
 it=('Nuovo ticket: {{.Ticket.Subject}}', hi('it') + 'è stato aperto un nuovo ticket da {{.Ticket.RequesterName}}.' + fields('it') + MSGS + op('it')),
 de=('Neues Ticket: {{.Ticket.Subject}}', hi('de') + '{{.Ticket.RequesterName}} hat ein neues Ticket eröffnet.' + fields('de') + MSGS + op('de')),
 fr=('Nouveau ticket : {{.Ticket.Subject}}', hi('fr') + 'un nouveau ticket a été ouvert par {{.Ticket.RequesterName}}.' + fields('fr') + MSGS + op('fr')),
 es=('Nuevo ticket: {{.Ticket.Subject}}', hi('es') + '{{.Ticket.RequesterName}} ha abierto un nuevo ticket.' + fields('es') + MSGS + op('es')),
)
add('staff.customer_replied',
 en=('Customer replied: {{.Ticket.Subject}}', hi('en') + 'the customer replied on ticket ' + REF + ':' + MSGS + op('en')),
 it=('Il cliente ha risposto: {{.Ticket.Subject}}', hi('it') + 'il cliente ha risposto al ticket ' + REF + ':' + MSGS + op('it')),
 de=('Kunde hat geantwortet: {{.Ticket.Subject}}', hi('de') + 'der Kunde hat auf das Ticket ' + REF + ' geantwortet:' + MSGS + op('de')),
 fr=('Le client a répondu : {{.Ticket.Subject}}', hi('fr') + 'le client a répondu sur le ticket ' + REF + ' :' + MSGS + op('fr')),
 es=('El cliente ha respondido: {{.Ticket.Subject}}', hi('es') + 'el cliente ha respondido en el ticket ' + REF + ':' + MSGS + op('es')),
)
add('staff.assigned',
 en=('Assigned to you: {{.Ticket.Subject}}', hi('en') + '{{.ActorName}} assigned the ticket ' + REF + ' to you.' + fields('en') + op('en')),
 it=('Assegnato a te: {{.Ticket.Subject}}', hi('it') + '{{.ActorName}} ti ha assegnato il ticket ' + REF + '.' + fields('it') + op('it')),
 de=('Ihnen zugewiesen: {{.Ticket.Subject}}', hi('de') + '{{.ActorName}} hat Ihnen das Ticket ' + REF + ' zugewiesen.' + fields('de') + op('de')),
 fr=('Assigné à vous : {{.Ticket.Subject}}', hi('fr') + '{{.ActorName}} vous a assigné le ticket ' + REF + '.' + fields('fr') + op('fr')),
 es=('Asignado a usted: {{.Ticket.Subject}}', hi('es') + '{{.ActorName}} le ha asignado el ticket ' + REF + '.' + fields('es') + op('es')),
)
REASON = {'en': 'Reason', 'it': 'Motivo', 'de': 'Grund', 'fr': 'Motif', 'es': 'Motivo'}
def reason(l): return f'{{{{with .Reason}}}}\n\n**{REASON[l]}:** {{{{.}}}}{{{{end}}}}'
add('staff.escalated',
 en=('Escalated: {{.Ticket.Subject}}', hi('en') + '{{.ActorName}} escalated the ticket ' + REF + ' from {{.FromTier}} to {{.ToTier}}.' + reason('en') + fields('en') + op('en')),
 it=('Escalation: {{.Ticket.Subject}}', hi('it') + '{{.ActorName}} ha inoltrato il ticket ' + REF + ' da {{.FromTier}} a {{.ToTier}}.' + reason('it') + fields('it') + op('it')),
 de=('Eskaliert: {{.Ticket.Subject}}', hi('de') + '{{.ActorName}} hat das Ticket ' + REF + ' von {{.FromTier}} nach {{.ToTier}} eskaliert.' + reason('de') + fields('de') + op('de')),
 fr=('Escaladé : {{.Ticket.Subject}}', hi('fr') + '{{.ActorName}} a escaladé le ticket ' + REF + ' de {{.FromTier}} à {{.ToTier}}.' + reason('fr') + fields('fr') + op('fr')),
 es=('Escalado: {{.Ticket.Subject}}', hi('es') + '{{.ActorName}} ha escalado el ticket ' + REF + ' de {{.FromTier}} a {{.ToTier}}.' + reason('es') + fields('es') + op('es')),
)
add('staff.mentioned',
 en=('You were mentioned: {{.Ticket.Subject}}', hi('en') + '{{.ActorName}} mentioned you in an internal note on ticket ' + REF + ':' + MSGS + op('en')),
 it=('Sei stato menzionato: {{.Ticket.Subject}}', hi('it') + '{{.ActorName}} ti ha menzionato in una nota interna sul ticket ' + REF + ':' + MSGS + op('it')),
 de=('Sie wurden erwähnt: {{.Ticket.Subject}}', hi('de') + '{{.ActorName}} hat Sie in einer internen Notiz zum Ticket ' + REF + ' erwähnt:' + MSGS + op('de')),
 fr=('Vous avez été mentionné : {{.Ticket.Subject}}', hi('fr') + '{{.ActorName}} vous a mentionné dans une note interne sur le ticket ' + REF + ' :' + MSGS + op('fr')),
 es=('Le han mencionado: {{.Ticket.Subject}}', hi('es') + '{{.ActorName}} le ha mencionado en una nota interna del ticket ' + REF + ':' + MSGS + op('es')),
)
SLA = {
 'en': ('SLA breached', 'SLA warning', 'first response', 'resolution', 'The {which} target of ticket ' + REF + ' {state} at {due} (UTC).', 'was missed', 'is due'),
 'it': ('SLA violato', 'Avviso SLA', 'prima risposta', 'risoluzione', 'L\'obiettivo di {which} del ticket ' + REF + ' {state} alle {due} (UTC).', 'è stato superato', 'scade'),
 'de': ('SLA verletzt', 'SLA-Warnung', 'Erstreaktion', 'Lösung', 'Das Ziel für {which} des Tickets ' + REF + ' {state} um {due} (UTC).', 'wurde verfehlt', 'ist fällig'),
 'fr': ('SLA dépassé', 'Alerte SLA', 'première réponse', 'résolution', 'L\'objectif de {which} du ticket ' + REF + ' {state} à {due} (UTC).', 'a été dépassé', 'arrive à échéance'),
 'es': ('SLA incumplido', 'Aviso de SLA', 'primera respuesta', 'resolución', 'El objetivo de {which} del ticket ' + REF + ' {state} a las {due} (UTC).', 'se ha incumplido', 'vence'),
}
def sla(l):
    br, wa, fr_, res, sent, missed, due = SLA[l]
    subj = f'{{{{if .SLABreached}}}}{br}{{{{else}}}}{wa}{{{{end}}}}: {{{{.Ticket.Subject}}}}'
    which = f'{{{{if eq .SLAWhich "first_response"}}}}{fr_}{{{{else}}}}{res}{{{{end}}}}'
    state = f'{{{{if .SLABreached}}}}{missed}{{{{else}}}}{due}{{{{end}}}}'
    body = hi(l) + sent.replace('{which}', which).replace('{state}', state).replace('{due}', '{{.SLADue}}') + fields(l) + op(l)
    return (subj, body)
add('staff.sla', **{l: sla(l) for l in SLA})
add('staff.children_resolved',
 en=('All child tickets resolved: {{.Ticket.Subject}}', hi('en') + 'all child tickets of ' + REF + ' are now resolved. You may want to resolve the parent ticket too.' + op('en')),
 it=('Tutti i ticket figli risolti: {{.Ticket.Subject}}', hi('it') + 'tutti i ticket figli di ' + REF + ' sono stati risolti. Potresti voler risolvere anche il ticket principale.' + op('it')),
 de=('Alle Unter-Tickets gelöst: {{.Ticket.Subject}}', hi('de') + 'alle Unter-Tickets von ' + REF + ' sind jetzt gelöst. Eventuell können Sie auch das übergeordnete Ticket lösen.' + op('de')),
 fr=('Tous les tickets enfants sont résolus : {{.Ticket.Subject}}', hi('fr') + 'tous les tickets enfants de ' + REF + ' sont désormais résolus. Vous pouvez aussi résoudre le ticket parent.' + op('fr')),
 es=('Todos los tickets hijos resueltos: {{.Ticket.Subject}}', hi('es') + 'todos los tickets hijos de ' + REF + ' ya están resueltos. Puede que también quiera resolver el ticket principal.' + op('es')),
)
add('staff.webhook_disabled',
 en=('Webhook disabled: {{.Vars.url}}', hi('en') + 'the webhook **{{.Vars.url}}** was disabled automatically ({{.Vars.reason}}) after {{.Vars.failures}} consecutive failed deliveries.\n\nFix the receiving endpoint, then re-enable the webhook in the administration settings of {{.Brand.ProductName}}.'),
 it=('Webhook disattivato: {{.Vars.url}}', hi('it') + 'il webhook **{{.Vars.url}}** è stato disattivato automaticamente ({{.Vars.reason}}) dopo {{.Vars.failures}} consegne fallite consecutive.\n\nCorreggi l\'endpoint ricevente, poi riattiva il webhook nelle impostazioni di amministrazione di {{.Brand.ProductName}}.'),
 de=('Webhook deaktiviert: {{.Vars.url}}', hi('de') + 'der Webhook **{{.Vars.url}}** wurde nach {{.Vars.failures}} aufeinanderfolgenden fehlgeschlagenen Zustellungen automatisch deaktiviert ({{.Vars.reason}}).\n\nBeheben Sie das Problem am Empfänger und aktivieren Sie den Webhook danach in den Administrationseinstellungen von {{.Brand.ProductName}} wieder.'),
 fr=('Webhook désactivé : {{.Vars.url}}', hi('fr') + 'le webhook **{{.Vars.url}}** a été désactivé automatiquement ({{.Vars.reason}}) après {{.Vars.failures}} échecs de livraison consécutifs.\n\nCorrigez le point de réception, puis réactivez le webhook dans les paramètres d\'administration de {{.Brand.ProductName}}.'),
 es=('Webhook desactivado: {{.Vars.url}}', hi('es') + 'el webhook **{{.Vars.url}}** se ha desactivado automáticamente ({{.Vars.reason}}) tras {{.Vars.failures}} entregas fallidas consecutivas.\n\nCorrija el punto de recepción y vuelva a activar el webhook en la configuración de administración de {{.Brand.ProductName}}.'),
)
EXP = {
 'en': 'This link expires in {{.Vars.expires_minutes}} minutes.',
 'it': 'Il link scade tra {{.Vars.expires_minutes}} minuti.',
 'de': 'Dieser Link läuft in {{.Vars.expires_minutes}} Minuten ab.',
 'fr': 'Ce lien expire dans {{.Vars.expires_minutes}} minutes.',
 'es': 'Este enlace caduca en {{.Vars.expires_minutes}} minutos.',
}
IGN = {
 'en': 'If you did not request this, you can ignore this email.',
 'it': 'Se non hai richiesto tu questa email, puoi ignorarla.',
 'de': 'Falls Sie dies nicht angefordert haben, können Sie diese E-Mail ignorieren.',
 'fr': 'Si vous n\'êtes pas à l\'origine de cette demande, vous pouvez ignorer cet e-mail.',
 'es': 'Si usted no lo ha solicitado, puede ignorar este correo.',
}
def auth(l, intro, label):
    return hi(l) + intro + f'\n\n[{label}]({{{{.Vars.url}}}})\n\n' + EXP[l] + ' ' + IGN[l]
add('auth.verify_email',
 en=('Verify your email address', auth('en', 'please confirm your email address for {{.Brand.ProductName}}.', 'Verify email address')),
 it=('Verifica il tuo indirizzo email', auth('it', 'conferma il tuo indirizzo email per {{.Brand.ProductName}}.', 'Verifica indirizzo email')),
 de=('Bestätigen Sie Ihre E-Mail-Adresse', auth('de', 'bitte bestätigen Sie Ihre E-Mail-Adresse für {{.Brand.ProductName}}.', 'E-Mail-Adresse bestätigen')),
 fr=('Vérifiez votre adresse e-mail', auth('fr', 'veuillez confirmer votre adresse e-mail pour {{.Brand.ProductName}}.', 'Vérifier l\'adresse e-mail')),
 es=('Verifique su dirección de correo', auth('es', 'confirme su dirección de correo para {{.Brand.ProductName}}.', 'Verificar dirección de correo')),
)
RESET = {
 'en': ('Reset your password', 'we received a request to reset your {{.Brand.ProductName}} password.', 'Choose a new password'),
 'it': ('Reimposta la tua password', 'abbiamo ricevuto una richiesta di reimpostazione della tua password di {{.Brand.ProductName}}.', 'Scegli una nuova password'),
 'de': ('Passwort zurücksetzen', 'wir haben eine Anfrage erhalten, Ihr Passwort für {{.Brand.ProductName}} zurückzusetzen.', 'Neues Passwort wählen'),
 'fr': ('Réinitialisez votre mot de passe', 'nous avons reçu une demande de réinitialisation de votre mot de passe {{.Brand.ProductName}}.', 'Choisir un nouveau mot de passe'),
 'es': ('Restablezca su contraseña', 'hemos recibido una solicitud para restablecer su contraseña de {{.Brand.ProductName}}.', 'Elegir una nueva contraseña'),
}
# SSO users (authn sends vars.sso="true", AUTH-015) have no local password:
# they get "sign in with SSO" text and no link at all.
SSO = {
 'en': 'Your account signs in with single sign-on (SSO), so it has no password here. Sign in with SSO on the login page; your password is managed by your identity provider.',
 'it': 'Il tuo account accede tramite single sign-on (SSO), quindi qui non ha una password. Accedi con SSO dalla pagina di accesso; la tua password è gestita dal tuo provider di identità.',
 'de': 'Ihr Konto meldet sich per Single Sign-On (SSO) an und hat hier kein Passwort. Melden Sie sich auf der Anmeldeseite mit SSO an; Ihr Passwort wird von Ihrem Identitätsanbieter verwaltet.',
 'fr': 'Votre compte se connecte par authentification unique (SSO) et n\'a donc pas de mot de passe ici. Connectez-vous avec le SSO depuis la page de connexion ; votre mot de passe est géré par votre fournisseur d\'identité.',
 'es': 'Su cuenta inicia sesión mediante inicio de sesión único (SSO), por lo que aquí no tiene contraseña. Acceda con SSO desde la página de inicio de sesión; su contraseña la gestiona su proveedor de identidad.',
}
def reset(l):
    subj, intro, label = RESET[l]
    return (subj, hi(l) + intro + '\n\n{{if eq (index .Vars "sso") "true"}}' + SSO[l] + '\n\n' + IGN[l]
            + '{{else}}' + f'[{label}]({{{{.Vars.url}}}})\n\n' + EXP[l] + ' ' + IGN[l] + '{{end}}')
add('auth.reset_password', **{l: reset(l) for l in RESET})
add('auth.magic_link',
 en=('Your sign-in link', auth('en', 'use the link below to sign in to {{.Brand.ProductName}}.', 'Sign in')),
 it=('Il tuo link di accesso', auth('it', 'usa il link qui sotto per accedere a {{.Brand.ProductName}}.', 'Accedi')),
 de=('Ihr Anmeldelink', auth('de', 'mit dem folgenden Link melden Sie sich bei {{.Brand.ProductName}} an.', 'Anmelden')),
 fr=('Votre lien de connexion', auth('fr', 'utilisez le lien ci-dessous pour vous connecter à {{.Brand.ProductName}}.', 'Se connecter')),
 es=('Su enlace de acceso', auth('es', 'utilice el siguiente enlace para acceder a {{.Brand.ProductName}}.', 'Acceder')),
)
add('auth.invite',
 en=('You have been invited to {{.Brand.ProductName}}', auth('en', '{{.Vars.inviter}} invited you to {{.Brand.ProductName}}.', 'Accept the invitation')),
 it=('Sei stato invitato su {{.Brand.ProductName}}', auth('it', '{{.Vars.inviter}} ti ha invitato su {{.Brand.ProductName}}.', 'Accetta l\'invito')),
 de=('Sie wurden zu {{.Brand.ProductName}} eingeladen', auth('de', '{{.Vars.inviter}} hat Sie zu {{.Brand.ProductName}} eingeladen.', 'Einladung annehmen')),
 fr=('Vous êtes invité sur {{.Brand.ProductName}}', auth('fr', '{{.Vars.inviter}} vous a invité sur {{.Brand.ProductName}}.', 'Accepter l\'invitation')),
 es=('Le han invitado a {{.Brand.ProductName}}', auth('es', '{{.Vars.inviter}} le ha invitado a {{.Brand.ProductName}}.', 'Aceptar la invitación')),
)
AR = {
 'en': ('You already have an account', 'someone tried to register at {{.Brand.ProductName}} with this email address, but an account already exists.', 'Sign in or reset your password', 'If this was not you, you can ignore this email.'),
 'it': ('Hai già un account', 'qualcuno ha provato a registrarsi su {{.Brand.ProductName}} con questo indirizzo email, ma esiste già un account.', 'Accedi o reimposta la password', 'Se non sei stato tu, puoi ignorare questa email.'),
 'de': ('Sie haben bereits ein Konto', 'jemand hat versucht, sich bei {{.Brand.ProductName}} mit dieser E-Mail-Adresse zu registrieren, es existiert aber bereits ein Konto.', 'Anmelden oder Passwort zurücksetzen', 'Falls Sie das nicht waren, können Sie diese E-Mail ignorieren.'),
 'fr': ('Vous avez déjà un compte', 'quelqu\'un a tenté de s\'inscrire sur {{.Brand.ProductName}} avec cette adresse e-mail, mais un compte existe déjà.', 'Se connecter ou réinitialiser le mot de passe', 'Si ce n\'était pas vous, vous pouvez ignorer cet e-mail.'),
 'es': ('Ya tiene una cuenta', 'alguien ha intentado registrarse en {{.Brand.ProductName}} con esta dirección de correo, pero ya existe una cuenta.', 'Acceder o restablecer la contraseña', 'Si no ha sido usted, puede ignorar este correo.'),
}
add('auth.already_registered', **{l: (v[0], hi(l) + v[1] + f'\n\n[{v[2]}]({{{{.Vars.url}}}})\n\n' + v[3]) for l, v in AR.items()})
# Two-factor sign-in code (D-19 §B, AUTH-035+): the code is the only secret, never a link.
MFA = {
 'en': ('Your sign-in code', 'use this code to finish signing in to {{.Brand.ProductName}}:', 'The code expires in {{.Vars.expires_minutes}} minutes and works only once.', 'If you did not try to sign in, change your password: someone else knows it.'),
 'it': ('Il tuo codice di accesso', 'usa questo codice per completare l\'accesso a {{.Brand.ProductName}}:', 'Il codice scade tra {{.Vars.expires_minutes}} minuti e si può usare una sola volta.', 'Se non hai provato ad accedere, cambia la password: qualcun altro la conosce.'),
 'de': ('Ihr Anmeldecode', 'mit diesem Code schließen Sie die Anmeldung bei {{.Brand.ProductName}} ab:', 'Der Code ist {{.Vars.expires_minutes}} Minuten gültig und kann nur einmal verwendet werden.', 'Falls Sie sich nicht anmelden wollten, ändern Sie Ihr Passwort: Jemand anderes kennt es.'),
 'fr': ('Votre code de connexion', 'utilisez ce code pour terminer votre connexion à {{.Brand.ProductName}} :', 'Le code expire dans {{.Vars.expires_minutes}} minutes et ne fonctionne qu\'une seule fois.', 'Si vous n\'avez pas essayé de vous connecter, changez votre mot de passe : quelqu\'un d\'autre le connaît.'),
 'es': ('Su código de acceso', 'utilice este código para completar el acceso a {{.Brand.ProductName}}:', 'El código caduca en {{.Vars.expires_minutes}} minutos y solo se puede usar una vez.', 'Si no ha intentado acceder, cambie su contraseña: otra persona la conoce.'),
}
add('auth.mfa_code', **{l: (v[0], hi(l) + v[1] + '\n\n**{{.Vars.code}}**\n\n' + v[2] + ' ' + v[3]) for l, v in MFA.items()})

# privacy.export_ready (D-19 §C): sent through mail.auth to whoever requested a data export.
PRV = {
 'en': ('Your data export is ready', 'the export of personal data you requested from {{.Brand.ProductName}} is ready.', 'Download the export', 'The file is deleted after {{.Vars.expires_days}} days. If you did not request this export, contact us.'),
 'it': ('La tua esportazione dei dati è pronta', "l'esportazione dei dati personali che hai richiesto a {{.Brand.ProductName}} è pronta.", "Scarica l'esportazione", 'Il file viene eliminato dopo {{.Vars.expires_days}} giorni. Se non hai richiesto tu questa esportazione, contattaci.'),
 'de': ('Ihr Datenexport ist bereit', 'der von Ihnen angeforderte Export Ihrer personenbezogenen Daten bei {{.Brand.ProductName}} ist bereit.', 'Export herunterladen', 'Die Datei wird nach {{.Vars.expires_days}} Tagen gelöscht. Falls Sie diesen Export nicht angefordert haben, kontaktieren Sie uns.'),
 'fr': ('Votre export de données est prêt', "l'export des données personnelles que vous avez demandé à {{.Brand.ProductName}} est prêt.", "Télécharger l'export", "Le fichier est supprimé après {{.Vars.expires_days}} jours. Si vous n'avez pas demandé cet export, contactez-nous."),
 'es': ('Su exportación de datos está lista', 'la exportación de datos personales que solicitó a {{.Brand.ProductName}} está lista.', 'Descargar la exportación', 'El archivo se elimina después de {{.Vars.expires_days}} días. Si no ha solicitado esta exportación, contáctenos.'),
}
add('privacy.export_ready', **{l: (v[0], hi(l) + v[1] + f'\n\n[{v[2]}]({{{{.Vars.url}}}})\n\n' + v[3]) for l, v in PRV.items()})

LAYOUT = {
 'en': ('Privacy policy', 'Terms', 'Imprint', 'Powered by', 'This is an automated message from'),
 'it': ('Informativa sulla privacy', 'Termini', 'Note legali', 'Realizzato con', 'Questo è un messaggio automatico di'),
 'de': ('Datenschutz', 'Nutzungsbedingungen', 'Impressum', 'Bereitgestellt mit', 'Dies ist eine automatische Nachricht von'),
 'fr': ('Confidentialité', 'Conditions', 'Mentions légales', 'Propulsé par', 'Ceci est un message automatique de'),
 'es': ('Privacidad', 'Condiciones', 'Aviso legal', 'Con la tecnología de', 'Este es un mensaje automático de'),
}

KEYS = ['customer.ticket_received', 'customer.reply', 'customer.resolved', 'staff.ticket_new', 'staff.customer_replied',
        'staff.assigned', 'staff.escalated', 'staff.mentioned', 'staff.sla', 'staff.children_resolved', 'staff.webhook_disabled',
        'auth.verify_email', 'auth.reset_password', 'auth.magic_link', 'auth.invite', 'auth.already_registered', 'auth.mfa_code',
        'privacy.export_ready']
assert sorted(KEYS) == sorted(T), set(KEYS) ^ set(T)
LOCALES = ['en', 'it', 'de', 'fr', 'es']
HEAD = '{{- /* Generated by gen_templates.py from one translation table. Edit there or keep placeholders identical across locales. */ -}}\n'
REVIEW = '{{- /* needs_review: machine-assisted translation, pending native-speaker review */ -}}\n'
for l in LOCALES:
    d = os.path.join(OUT, l)
    os.makedirs(d, exist_ok=True)
    head = HEAD + (REVIEW if l in ('de', 'fr', 'es') else '')
    for k in KEYS:
        subj, body = T[k][l]
        with open(os.path.join(d, k + '.tmpl'), 'w') as f:
            f.write(head + '{{define "subject"}}' + subj + '{{end}}\n{{define "body"}}' + body + '\n{{end}}\n')
    p, t, i, pw, auto = LAYOUT[l]
    by = {'en': 'by', 'it': 'di', 'de': 'von', 'fr': 'par', 'es': 'por'}[l]
    with open(os.path.join(d, '_layout.tmpl'), 'w') as f:
        f.write(head + f'{{{{define "privacy"}}}}{p}{{{{end}}}}\n{{{{define "terms"}}}}{t}{{{{end}}}}\n{{{{define "imprint"}}}}{i}{{{{end}}}}\n'
                f'{{{{define "powered_by"}}}}{pw}{{{{end}}}}\n{{{{define "by_author"}}}}{by}{{{{end}}}}\n{{{{define "automated"}}}}{auto}{{{{end}}}}\n')
print('ok', len(KEYS) * len(LOCALES), 'templates')
