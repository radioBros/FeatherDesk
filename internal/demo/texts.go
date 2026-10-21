// SPDX-License-Identifier: AGPL-3.0-only

package demo

import "fmt"

type topic struct {
	cat, subject, body string
}

var topics = []topic{
	{"tech", "VPN keeps disconnecting every few minutes", "Since the update on Monday my VPN client drops the connection roughly every ten minutes. I have to reconnect by hand each time and lose my remote session."},
	{"tech", "Cannot open the shared drive from my Mac", "After upgrading macOS the shared drive no longer mounts. Finder says *The server could not be found*. Windows colleagues are fine."},
	{"tech", "App crashes on start after update 4.2", "Since version 4.2 the desktop app closes right after the splash screen. Reinstalling did not help. The crash report is attached."},
	{"tech", "Dashboard is very slow when filtering by date", "Filtering the dashboard by *last 90 days* takes more than a minute. Shorter ranges are fine."},
	{"tech", "Export to CSV fails with a timeout", "Exporting more than 5,000 rows to CSV ends with *Request timed out*. This worked last month."},
	{"tech", "API returns 502 on /v2/orders", "Our integration gets a **502 Bad Gateway** from `/v2/orders` about every tenth call, mostly around 09:00 CET."},
	{"tech", "Single sign-on loops back to the sign-in page", "After entering my credentials in the identity provider I land on the sign-in page again. Other people in the team can sign in."},
	{"tech", "Shared printer on the 3rd floor is offline", "The printer shows as *offline* on every laptop although it is switched on and the display is fine."},
	{"tech", "Email sync stopped on Android", "The mail app on my phone has not synced since yesterday afternoon. Webmail works."},
	{"tech", "Webhook deliveries arrive twice", "Our receiver gets every `ticket.created` webhook two times, a few seconds apart."},
	{"general", "How do I add a colleague to our account?", "A new colleague starts on Monday. What is the easiest way to give her access to the portal?"},
	{"general", "Where can I find the user manual?", "Is there a PDF version of the manual that I can share with our new hires?"},
	{"general", "Opening hours over the holidays", "Will support be available between Christmas and New Year? We have a release planned for the 28th."},
	{"general", "Change the company name in our profile", "We were renamed after a merger. How can we update the company name shown on tickets?"},
	{"access", "Password reset email is not arriving", "I requested a password reset three times and nothing arrives, not even in spam."},
	{"access", "Locked out after too many attempts", "I mistyped my password a few times and now the portal says my account is locked."},
	{"access", "Need access for a new employee", "Please create an account for our new colleague. Her work address is on the same domain."},
	{"access", "Verification code is not accepted", "The six-digit code from the authenticator app is rejected every time, although my phone time is correct."},
	{"billing", "Invoice shows the wrong VAT number", "The VAT number on invoice INV-2026-0412 is outdated. Please reissue it with the number from our updated profile."},
	{"billing", "Charged twice for the October subscription", "Our card was charged twice on 3 October. Please refund the duplicate payment."},
	{"billing", "Refund request for unused licences", "We cancelled 12 licences in September and were still billed for them. Can you credit us?"},
	{"billing", "Update billing address and PO number", "Please change the billing address and add the purchase order number PO-77831 to future invoices."},
	{"billing", "Copy of last quarter's invoices", "Our auditors need copies of all invoices from July to September as PDF."},
	{"feature", "Dark mode for the portal", "Many of us work late. A dark theme for the portal would be great."},
	{"feature", "Bulk edit tickets in the agent panel", "It would save a lot of time to change the priority of several selected tickets in one go."},
	{"feature", "Export reports as PDF", "Our management wants the monthly summary as a PDF instead of a screenshot."},
}

var agentReplies = []string{
	"Hi %s, thanks for reaching out. I am looking into this now and will come back to you shortly.",
	"Hello %s, I could reproduce the behaviour on our side. I am checking with a colleague and will update you today.",
	"Hi %s, could you tell me the exact time it last happened and whether you were on Wi-Fi or a cable? That helps me narrow it down.",
	"Hi %s, thank you for the details. I have applied a change on our side; please try again and let me know.",
	"Hello %s, a quick update: the fix is being rolled out this afternoon. I will confirm as soon as it is live.",
	"Hi %s, good news: we found the cause and corrected it. You can continue as usual.",
}

var customerReplies = []string{
	"Thanks for the quick answer. It happened again this morning around 09:15, on Wi-Fi.",
	"I tried that and it did not change anything. Screenshot attached to my previous message.",
	"Yes, it is still happening. This is blocking our team today, can you please prioritise it?",
	"That works now, thank you! I will keep an eye on it for a few days.",
	"Understood. Please let me know if you need anything else from my side.",
}

var internalNotes = []string{
	"Reproduced on staging with build 4.2.1; looks related to the session refresh change.",
	"Customer is on the premium plan; handle with care. Last incident was in August.",
	"Checked the logs: repeated 502 from the upstream proxy between 08:55 and 09:10.",
	"Waiting for the vendor to confirm the certificate renewal; will check again tomorrow morning.",
	"Not a product bug: the customer's firewall blocks UDP 4500. Documented the workaround in the KB.",
	"Double-checked the invoice with finance; the VAT number was changed on 1 September.",
}

var escalationReasons = []string{
	"Needs access to the production configuration, which L1 does not have.",
	"Reproducible bug in the sync service; engineering has to look at the logs.",
	"Affects several users of the same organization; a specialist should coordinate.",
	"Customer asks for a root-cause analysis; needs an expert.",
}

var resolutionTexts = []string{
	"The root cause was a misconfigured proxy timeout. It is fixed and you should not see the issue again. I am marking this as resolved; reply if it comes back.",
	"We corrected the invoice and sent you the new PDF. I am marking this as resolved; reply if something is still off.",
	"The account is unlocked and the reset email was sent from a different route. I am marking this as resolved.",
	"This is fixed in version 4.2.2, which is now available. Resolving the ticket; reach out if you still see the problem.",
}

var csatComments = map[int][]string{
	5: {"Fast and friendly, thank you!", "Solved in no time.", ""},
	4: {"Good service.", ""},
	3: {"It worked out in the end.", ""},
	2: {"Took too long to get a first answer.", "I had to explain the problem twice."},
	1: {"Not solved the first time and nobody called back.", ""},
}

func pick[T any](s *seeder, list []T) T { return list[s.rng.IntN(len(list))] }

func agentReply(s *seeder, name string) string {
	return fmt.Sprintf(pick(s, agentReplies), firstName(name))
}

func firstName(n string) string {
	for i, r := range n {
		if r == ' ' {
			return n[:i]
		}
	}
	return n
}
