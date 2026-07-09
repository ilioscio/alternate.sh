package shell

// FederationNotifier is how shell commands hand work to the federation
// workers without the shell depending on the server package: queued mail
// wants an immediate delivery attempt, and news posts/cancels push to peers.
type FederationNotifier interface {
	MailQueued()
	ArticlePosted(articleID string)
	ArticleCancelled(articleID string)
	// PeerAdded fires after `node add`: sync news from the new peer right
	// away and flush any mail queued while it was unreachable.
	PeerAdded()
}

// Federation is set by main at startup iff federation is enabled; commands
// must nil-check. A single process hosts exactly one node, so a package
// variable is the honest shape of this dependency.
var Federation FederationNotifier
