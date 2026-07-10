package shell

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// notifyUser sends a write-style system notice (moderation outcomes).
func notifyUser(s *Session, username, message string) {
	s.hub.Send(username, presence.WriteNotice{
		Kind:    presence.NoticeWrite,
		From:    "news",
		Message: message,
	})
}

func cmdNews(s *Session, args []string) error {
	return browseGroups(s)
}

func cmdPost(s *Session, args []string) error {
	var groupName string
	if len(args) > 0 {
		groupName = args[0]
	} else {
		s.Print("Newsgroup: ")
		rl := s.newRL()
		groupName, _ = rl.ReadLine("")
	}
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return nil
	}

	group, err := db.GetNewsgroupByName(s.ctx, s.db, groupName)
	if err != nil {
		s.Printf("post: %s: no such newsgroup\r\n", groupName)
		return nil
	}
	postArticle(s, group, nil, "")
	return nil
}

func browseGroups(s *Session) error {
	groups, err := db.GetNewsgroups(s.ctx, s.db, s.User.ID)
	if err != nil {
		s.Println("news: error reading newsgroups")
		return nil
	}
	if len(groups) == 0 {
		s.Println("No newsgroups configured.")
		return nil
	}

	totalUnread := 0
	for _, g := range groups {
		totalUnread += g.Unread
	}
	s.Printf("Newsgroups — %d groups, %d unread\r\n\r\n", len(groups), totalUnread)
	printGroupList(s, groups)
	printModQueueHint(s)

	rl := s.newRL()
	for {
		prompt := "\r\nEnter group name ('q' to quit): "
		if s.User.Admin {
			prompt = "\r\nEnter group name ('mq' for the moderation queue, 'q' to quit): "
		}
		s.Print(prompt)
		line, err := rl.ReadLine("")
		if err != nil || strings.TrimSpace(line) == "q" {
			return nil
		}
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if name == "mq" && s.User.Admin {
			reviewQueue(s)
			continue
		}

		// Find group by name (exact or prefix)
		var chosen *db.Newsgroup
		for i := range groups {
			if groups[i].Name == name || strings.HasPrefix(groups[i].Name, name) {
				chosen = &groups[i]
				break
			}
		}
		if chosen == nil {
			s.Printf("news: %s: no such newsgroup\r\n", name)
			printGroupList(s, groups)
			continue
		}

		browseGroup(s, chosen)

		// Refresh group list after reading
		groups, _ = db.GetNewsgroups(s.ctx, s.db, s.User.ID)
		totalUnread = 0
		for _, g := range groups {
			totalUnread += g.Unread
		}
		if totalUnread > 0 {
			s.Printf("\r\n%d unread articles remain.\r\n", totalUnread)
		}
		printGroupList(s, groups)
	}
}

// printModQueueHint tells admins when articles await review.
func printModQueueHint(s *Session) {
	if !s.User.Admin {
		return
	}
	if pending, err := db.PendingArticles(s.ctx, s.db); err == nil && len(pending) > 0 {
		s.Printf("\r\n  [%d article(s) awaiting moderation — type 'mq' to review]\r\n", len(pending))
	}
}

func printGroupList(s *Session, groups []db.Newsgroup) {
	s.Printf("  %-28s  %6s  %6s  %s\r\n", "Name", "Unread", "Total", "Description")
	s.HLine()
	for _, g := range groups {
		marker := " "
		if g.Unread > 0 {
			marker = "*"
		}
		name := g.Name
		if len(name) > 28 {
			name = name[:25] + "..."
		}
		desc := g.Description
		if len(desc) > 34 {
			desc = desc[:31] + "..."
		}
		s.Printf("  %s%-28s  %6d  %6d  %s\r\n", marker, name, g.Unread, g.Total, desc)
	}
	s.HLine()
}

func browseGroup(s *Session, group *db.Newsgroup) {
	arts, err := db.GetArticles(s.ctx, s.db, group.ID, s.User.ID)
	if err != nil {
		s.Println("news: error reading articles")
		return
	}

	if len(arts) == 0 {
		s.Printf("%s — no articles yet. Be the first to post!\r\n", group.Name)
		s.Printf("Commands: (p)ost, (q)uit\r\n")
	} else {
		unread := 0
		for _, a := range arts {
			if !a.Read {
				unread++
			}
		}
		s.Printf("\r\n%s — %s\r\n%d unread of %d articles\r\n\r\n",
			group.Name, group.Description, unread, len(arts))
		printArticleList(s, arts)
	}

	rl := s.newRL()
	current := -1

	for {
		s.Print("\r\n[number, f<n>=followup, c<n>=cancel, p=post, m=mark all read, q=quit]\r\n? ")
		line, err := rl.ReadLine("")
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)

		switch {
		case line == "q" || line == "quit":
			return

		case line == "p" || line == "post":
			postArticle(s, group, nil, "")
			arts, _ = db.GetArticles(s.ctx, s.db, group.ID, s.User.ID)
			printArticleList(s, arts)

		case line == "m":
			db.MarkGroupRead(s.ctx, s.db, group.ID, s.User.ID)
			for i := range arts {
				arts[i].Read = true
			}
			s.Println("All articles marked as read.")

		case strings.HasPrefix(line, "c"):
			n, ok := parseNewsNum(strings.TrimSpace(line[1:]), len(arts))
			if !ok && current >= 0 {
				n, ok = current+1, true
			}
			if !ok {
				s.Println("Usage: c<number>")
				break
			}
			a := arts[n-1]
			if !confirm(s, fmt.Sprintf("Cancel article #%s (%s)? [y/n]: ", shortID(a.ID), a.Subject)) {
				s.Println("Not cancelled.")
				break
			}
			cancelled, err := db.CancelArticle(s.ctx, s.db, a.ID, s.User.ID, s.User.Admin)
			if err != nil {
				s.Println("cancel: error cancelling article")
				break
			}
			if !cancelled {
				s.Println("cancel: you can only cancel your own articles")
				break
			}
			s.Println("Article cancelled.")
			// Admins cancelling someone else's article is a moderation act.
			if s.User.Admin && (a.AuthorID == nil || *a.AuthorID != s.User.ID) {
				db.RecordAudit(s.ctx, s.db, s.User.ID, "article.cancel", a.AuthorName,
					a.GroupName+": "+a.Subject)
			}
			// Retract from peers — but only for articles this node authored:
			// a locally-cancelled remote article stays local moderation (§10.5).
			if Federation != nil && a.OriginNode == nil {
				Federation.ArticleCancelled(a.ID)
			}
			current = -1
			arts, _ = db.GetArticles(s.ctx, s.db, group.ID, s.User.ID)
			if len(arts) == 0 {
				s.Printf("%s — no articles remain.\r\n", group.Name)
			} else {
				printArticleList(s, arts)
			}

		case strings.HasPrefix(line, "f"):
			n, ok := parseNewsNum(strings.TrimSpace(line[1:]), len(arts))
			if !ok && current >= 0 {
				n, ok = current+1, true
			}
			if ok {
				a := arts[n-1]
				// Find root of thread for followup subject
				subj := a.Subject
				if !strings.HasPrefix(strings.ToLower(subj), "re:") {
					subj = "Re: " + subj
				}
				id := a.ID
				postArticle(s, group, &id, subj)
				arts, _ = db.GetArticles(s.ctx, s.db, group.ID, s.User.ID)
				printArticleList(s, arts)
			} else {
				s.Println("Usage: f<number>")
			}

		default:
			if n, ok := parseNewsNum(line, len(arts)); ok {
				current = n - 1
				showArticle(s, &arts[current])
				db.MarkArticleRead(s.ctx, s.db, arts[current].ID, s.User.ID)
				arts[current].Read = true

				// Inline followup prompt
				s.Print("[f=followup, n=next unread, q=back]: ")
				cmd, _ := rl.ReadLine("")
				switch strings.TrimSpace(cmd) {
				case "f":
					subj := arts[current].Subject
					if !strings.HasPrefix(strings.ToLower(subj), "re:") {
						subj = "Re: " + subj
					}
					id := arts[current].ID
					postArticle(s, group, &id, subj)
					arts, _ = db.GetArticles(s.ctx, s.db, group.ID, s.User.ID)
					printArticleList(s, arts)
				case "n":
					for i := current + 1; i < len(arts); i++ {
						if !arts[i].Read {
							current = i
							showArticle(s, &arts[current])
							db.MarkArticleRead(s.ctx, s.db, arts[current].ID, s.User.ID)
							arts[current].Read = true
							break
						}
					}
				}
			} else if line != "" {
				s.Println("Unknown command.")
			}
		}
	}
}

func printArticleList(s *Session, arts []db.Article) {
	s.Printf("  %-3s  %-38s  %-14s  %s\r\n", "N", "Subject", "Author", "Date")
	s.HLine()
	for i, a := range arts {
		unread := " "
		if !a.Read {
			unread = "*"
		}
		indent := ""
		if a.Depth > 0 {
			indent = strings.Repeat("  ", min2(a.Depth, 3))
		}
		subj := indent + a.Subject
		if len(subj) > 38 {
			subj = subj[:35] + "..."
		}
		author := a.AuthorName
		if len(author) > 14 {
			author = author[:14]
		}
		s.Printf("  %s%3d  %-38s  %-14s  %s\r\n",
			unread, i+1, subj, author,
			a.CreatedAt.Local().Format("Jan 2"),
		)
	}
	s.HLine()
}

func showArticle(s *Session, a *db.Article) {
	s.Println("")
	s.HLine()
	s.Printf("  %s #%s — %s\r\n", a.GroupName, shortID(a.ID), a.Subject)
	s.Printf("  From: %s · %s\r\n", a.AuthorName, a.CreatedAt.Local().Format("Mon Jan 2 15:04 MST 2006"))
	if a.ParentID != nil {
		s.Printf("  (reply to #%s)\r\n", shortID(*a.ParentID))
	}
	s.HLine()
	s.Println("")
	for _, line := range strings.Split(a.Body, "\n") {
		s.Printf("  %s\r\n", line)
	}
	s.Println("")
}

func postArticle(s *Session, group *db.Newsgroup, parentID *string, subject string) {
	// Non-admin posts to moderated groups go to the approval queue rather
	// than being refused (§10.5).
	needsApproval := group.Moderated && !s.User.Admin

	// Anti-spam: cap articles per day for non-admins.
	if !s.User.Admin && s.cfg.Limits.NewsPerDay > 0 {
		n, _ := db.CountArticlesPostedSince(s.ctx, s.db, s.User.ID, "24 hours")
		if n >= s.cfg.Limits.NewsPerDay {
			s.Printf("post: daily posting limit reached (%d/day). Try again tomorrow.\r\n", s.cfg.Limits.NewsPerDay)
			return
		}
	}

	s.Printf("Posting to: %s\r\n", group.Name)

	if subject == "" {
		s.Print("Subject: ")
		rl := s.newRL()
		subject, _ = rl.ReadLine("")
		if subject == "" {
			s.Println("Cancelled.")
			return
		}
	} else {
		s.Printf("Subject: %s\r\n", subject)
	}

	body := readBody(s, "Article body (end with '.' on a line by itself):")
	if body == "" {
		s.Println("Cancelled — empty article.")
		return
	}

	// Append signature
	if s.User.Signature != "" {
		body += "\n\n-- \n" + s.User.Signature
	}

	if !confirm(s, fmt.Sprintf("Post to %s? [y/n]: ", group.Name)) {
		s.Println("Cancelled.")
		return
	}

	a, err := db.PostArticle(s.ctx, s.db, group.ID, s.User.ID, subject, body, parentID, !needsApproval)
	if err != nil {
		s.Println("post: error posting article")
		return
	}
	if needsApproval {
		s.Printf("Article submitted for moderator review — it appears in %s once approved.\r\n", group.Name)
		return
	}
	s.Println("Article posted.")

	// Offer the article to peers (§8.4); the push path re-checks whether
	// the group federates, and catch-up sync covers unreachable peers.
	if Federation != nil {
		Federation.ArticlePosted(a.ID)
	}
}

// reviewQueue is the admin moderation queue: every unapproved article,
// oldest first, approve or reject one at a time.
func reviewQueue(s *Session) {
	pending, err := db.PendingArticles(s.ctx, s.db)
	if err != nil {
		s.Println("news: error reading the moderation queue")
		return
	}
	if len(pending) == 0 {
		s.Println("Moderation queue is empty.")
		return
	}

	rl := s.newRL()
	for i, a := range pending {
		s.Println("")
		s.HLine()
		s.Printf("  moderation queue — %d of %d\r\n", i+1, len(pending))
		s.HLine()
		s.Printf("  Group:   %s\r\n", a.GroupName)
		s.Printf("  From:    %s\r\n", a.AuthorName)
		s.Printf("  Date:    %s\r\n", a.CreatedAt.Local().Format("Mon Jan 2 15:04"))
		s.Printf("  Subject: %s\r\n", a.Subject)
		s.HLine()
		for _, line := range strings.Split(a.Body, "\n") {
			s.Printf("  %s\r\n", line)
		}
		s.HLine()

		s.Print("[a=approve, r=reject, s=skip, q=quit] ? ")
		line, err := rl.ReadLine("")
		if err != nil {
			return
		}
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "a":
			if ok, err := db.ReviewArticle(s.ctx, s.db, a.ID, true); err != nil || !ok {
				s.Println("news: could not approve")
				continue
			}
			s.Printf("Approved into %s.\r\n", a.GroupName)
			db.RecordAudit(s.ctx, s.db, s.User.ID, "article.approve", a.AuthorName,
				a.GroupName+": "+a.Subject)
			notifyUser(s, a.AuthorName,
				fmt.Sprintf("news: your article %q was approved into %s", a.Subject, a.GroupName))
			// Approved articles federate like fresh posts.
			if Federation != nil {
				Federation.ArticlePosted(a.ID)
			}
		case "r":
			if ok, err := db.ReviewArticle(s.ctx, s.db, a.ID, false); err != nil || !ok {
				s.Println("news: could not reject")
				continue
			}
			s.Println("Rejected.")
			db.RecordAudit(s.ctx, s.db, s.User.ID, "article.reject", a.AuthorName,
				a.GroupName+": "+a.Subject)
			notifyUser(s, a.AuthorName,
				fmt.Sprintf("news: your article %q was not approved for %s", a.Subject, a.GroupName))
		case "q":
			return
		default: // skip
			continue
		}
	}
	s.Println("(end of queue)")
}

func parseNewsNum(s string, max int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > max {
		return 0, false
	}
	return n, true
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
