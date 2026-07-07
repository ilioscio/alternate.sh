package valid

// disposableDomains is a bundled blocklist of common throwaway/disposable
// email providers. It is intentionally a starting set, not exhaustive — the
// primary bot defense is email confirmation plus per-IP rate limiting; this
// just trims the most common churn-account sources.
//
// To refresh: pull from a maintained list (e.g. the disposable-email-domains
// project) and regenerate this map. Kept as an in-binary map so there is no
// runtime dependency or network fetch.
var disposableDomains = map[string]bool{
	"mailinator.com":     true,
	"guerrillamail.com":  true,
	"guerrillamail.info": true,
	"grr.la":             true,
	"sharklasers.com":    true,
	"10minutemail.com":   true,
	"10minutemail.net":   true,
	"tempmail.com":       true,
	"temp-mail.org":      true,
	"throwawaymail.com":  true,
	"getnada.com":        true,
	"nada.email":         true,
	"trashmail.com":      true,
	"trashmail.de":       true,
	"yopmail.com":        true,
	"maildrop.cc":        true,
	"dispostable.com":    true,
	"fakeinbox.com":      true,
	"mailnesia.com":      true,
	"mohmal.com":         true,
	"emailondeck.com":    true,
	"tempinbox.com":      true,
	"spamgourmet.com":    true,
	"mytemp.email":       true,
	"33mail.com":         true,
	"burnermail.io":      true,
	"mailcatch.com":      true,
	"tempr.email":        true,
	"discard.email":      true,
	"inboxkitten.com":    true,
}

// IsDisposableDomain reports whether a (lowercased) domain is in the blocklist.
// Exported for reuse/testing.
func IsDisposableDomain(domain string) bool {
	return disposableDomains[domain]
}
