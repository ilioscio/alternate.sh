package shell

import (
	"fmt"
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/valid"
)

// Mailing lists (§5.4): admin-created, self-service membership, and mail to
// a list name fans out to every subscriber. Early mailing-list culture,
// local to each node.

func cmdLists(s *Session, args []string) error {
	if len(args) == 0 {
		return listsShow(s)
	}
	switch args[0] {
	case "create":
		return listsCreate(s, args[1:])
	case "rm", "remove", "delete":
		return listsRemove(s, args[1:])
	case "flag":
		return listsFlag(s, args[1:])
	default:
		usageError(s, "lists", "[create <name> [description] | rm <name> | flag <name> admin-only|open]")
		return nil
	}
}

func listsShow(s *Session) error {
	lists, err := db.ListMailingLists(s.ctx, s.db, s.User.ID)
	if err != nil {
		s.Println("lists: error reading lists")
		return nil
	}
	if len(lists) == 0 {
		s.Println("No mailing lists yet.")
		if s.User.Admin {
			s.Println("Create one with: lists create <name> [description]")
		}
		return nil
	}

	s.Printf("  %-16s %7s  %-9s  %s\r\n", "list", "members", "you", "description")
	s.HLine()
	for _, l := range lists {
		you := "-"
		if l.Subscribed {
			you = "member"
		}
		desc := l.Description
		if l.AdminOnlyPost {
			desc = "[announce-only] " + desc
		}
		s.Printf("  %-16s %7d  %-9s  %s\r\n", l.Name, l.Members, you, desc)
	}
	s.HLine()
	s.Println("  subscribe <list> / unsubscribe <list> to manage; 'mail <list>' posts to it")
	return nil
}

func listsCreate(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("lists: creating lists is admin-only")
		return nil
	}
	if len(args) < 1 {
		usageError(s, "lists", "create <name> [description]")
		return nil
	}
	name := strings.ToLower(args[0])
	description := strings.Join(args[1:], " ")

	// Lists share the username namespace: 'mail staff' must be unambiguous.
	if err := valid.ValidateUsername(name); err != nil {
		s.Printf("lists: bad name: %v\r\n", err)
		return nil
	}
	if _, err := db.GetUserByUsername(s.ctx, s.db, name); err == nil {
		s.Printf("lists: %s is a user — pick another name\r\n", name)
		return nil
	}
	if err := db.CreateMailingList(s.ctx, s.db, name, description); err != nil {
		s.Printf("lists: could not create %s (already exists?)\r\n", name)
		return nil
	}
	db.RecordAudit(s.ctx, s.db, s.User.ID, "list.create", name, description)
	s.Printf("List %s created. Users join with: subscribe %s\r\n", name, name)
	return nil
}

func listsRemove(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("lists: removing lists is admin-only")
		return nil
	}
	if len(args) != 1 {
		usageError(s, "lists", "rm <name>")
		return nil
	}
	ok, err := db.DeleteMailingList(s.ctx, s.db, args[0])
	if err != nil || !ok {
		s.Printf("lists: %s: no such list\r\n", args[0])
		return nil
	}
	db.RecordAudit(s.ctx, s.db, s.User.ID, "list.remove", args[0], "")
	s.Printf("List %s removed.\r\n", args[0])
	return nil
}

func listsFlag(s *Session, args []string) error {
	if !s.User.Admin {
		s.Println("lists: flagging lists is admin-only")
		return nil
	}
	if len(args) != 2 || (args[1] != "admin-only" && args[1] != "open") {
		usageError(s, "lists", "flag <name> admin-only|open")
		return nil
	}
	adminOnly := args[1] == "admin-only"
	ok, err := db.SetMailingListAdminOnly(s.ctx, s.db, args[0], adminOnly)
	if err != nil || !ok {
		s.Printf("lists: %s: no such list\r\n", args[0])
		return nil
	}
	db.RecordAudit(s.ctx, s.db, s.User.ID, "list.flag", args[0], args[1])
	s.Printf("List %s is now %s.\r\n", args[0], args[1])
	return nil
}

func cmdSubscribe(s *Session, args []string) error {
	return setSubscription(s, args, true)
}

func cmdUnsubscribe(s *Session, args []string) error {
	return setSubscription(s, args, false)
}

func setSubscription(s *Session, args []string, join bool) error {
	verb := map[bool]string{true: "subscribe", false: "unsubscribe"}[join]
	if len(args) != 1 {
		usageError(s, verb, "<list>")
		return nil
	}
	l, err := db.GetMailingList(s.ctx, s.db, strings.ToLower(args[0]), s.User.ID)
	if err != nil {
		s.Printf("%s: %s: no such list ('lists' shows them all)\r\n", verb, args[0])
		return nil
	}
	if join {
		changed, err := db.Subscribe(s.ctx, s.db, l.ID, s.User.ID)
		if err != nil {
			s.Printf("%s: error\r\n", verb)
			return nil
		}
		if !changed {
			s.Printf("You're already on %s.\r\n", l.Name)
			return nil
		}
		s.Printf("Subscribed to %s. Mail to it lands in your mailbox with a [%s] prefix.\r\n", l.Name, l.Name)
		return nil
	}
	changed, err := db.Unsubscribe(s.ctx, s.db, l.ID, s.User.ID)
	if err != nil {
		s.Printf("%s: error\r\n", verb)
		return nil
	}
	if !changed {
		s.Printf("You weren't on %s.\r\n", l.Name)
		return nil
	}
	s.Printf("Unsubscribed from %s.\r\n", l.Name)
	return nil
}

// composeListMail handles `mail <list>`: permission check, compose, fan out
// one message per subscriber with the [list] subject prefix.
func composeListMail(s *Session, l *db.MailingList) error {
	if l.AdminOnlyPost && !s.User.Admin {
		s.Printf("mail: %s is announce-only — admins post, everyone reads\r\n", l.Name)
		return nil
	}
	if !l.Subscribed && !s.User.Admin {
		s.Printf("mail: subscribe to %s before posting to it\r\n", l.Name)
		return nil
	}

	ids, usernames, err := db.ListMemberIDs(s.ctx, s.db, l.ID)
	if err != nil {
		s.Println("mail: error reading list members")
		return nil
	}
	if len(ids) == 0 {
		s.Printf("mail: %s has no subscribers yet\r\n", l.Name)
		return nil
	}

	s.Printf("To: %s (%d subscriber%s)\r\n", l.Name, len(ids), map[bool]string{true: "", false: "s"}[len(ids) == 1])
	s.Print("Subject: ")
	rl := s.newRL()
	subject, _ := rl.ReadLine("")
	if subject == "" {
		subject = "(no subject)"
	}
	subject = fmt.Sprintf("[%s] %s", l.Name, subject)

	body := readBody(s, "Message (end with '.' on a line by itself):")
	if body == "" {
		s.Println("Cancelled — empty message.")
		return nil
	}
	if s.User.Signature != "" {
		body += "\n\n-- \n" + s.User.Signature
	}
	if !confirm(s, fmt.Sprintf("Send to the %d member(s) of %s? [y/n]: ", len(ids), l.Name)) {
		s.Println("Cancelled.")
		return nil
	}

	sent := 0
	for i, id := range ids {
		if _, err := db.SendMail(s.ctx, s.db, s.User.ID, id, subject, body, nil); err == nil {
			sent++
			if usernames[i] != s.User.Username {
				notifyNewMail(s, usernames[i], s.User.Username, subject)
			}
		}
	}
	s.Printf("Message sent to %d member(s) of %s.\r\n", sent, l.Name)
	return nil
}
