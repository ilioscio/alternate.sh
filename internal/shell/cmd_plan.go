package shell

import (
	"strings"

	"github.com/ilioscio/alternate.sh/internal/db"
)

func cmdPlan(s *Session, args []string) error {
	// With no args, open editor for ~/.plan
	s.Printf("Current plan:\r\n")
	if s.User.Plan == "" {
		s.Println("  (none)")
	} else {
		printWrapped(s, s.User.Plan)
	}
	s.Println("")
	s.Println("Enter new plan (end with '.' on a line by itself, blank to clear):")

	rl := NewReadline(s.r, s.w)
	var lines []string
	for {
		line, err := rl.ReadLine("")
		if err != nil || line == "." {
			break
		}
		lines = append(lines, line)
	}
	plan := strings.Join(lines, "\n")
	if err := db.UpdatePlan(s.ctx, s.db, s.User.ID, plan); err != nil {
		s.Println("error saving plan")
		return nil
	}
	s.User.Plan = plan
	if plan == "" {
		s.Println("Plan cleared.")
	} else {
		s.Println("Plan updated.")
	}
	return nil
}

func cmdProject(s *Session, args []string) error {
	if len(args) > 0 {
		project := strings.Join(args, " ")
		if err := db.UpdateProject(s.ctx, s.db, s.User.ID, project); err != nil {
			s.Println("error saving project")
			return nil
		}
		s.User.Project = project
		s.Printf("Project set to: %s\r\n", project)
		return nil
	}

	s.Printf("Current project: %s\r\n", s.User.Project)
	s.Print("New project (blank to clear): ")
	rl := NewReadline(s.r, s.w)
	project, err := rl.ReadLine("")
	if err != nil {
		return nil
	}
	if err := db.UpdateProject(s.ctx, s.db, s.User.ID, project); err != nil {
		s.Println("error saving project")
		return nil
	}
	s.User.Project = project
	return nil
}

func cmdPublic(s *Session, args []string) error {
	if len(args) > 0 {
		// Read another user's public page
		target := args[0]
		page, err := db.GetPublicPage(s.ctx, s.db, target)
		if err != nil {
			s.Printf("public: %s: %s\r\n", target, err)
			return nil
		}
		s.HLine()
		s.Printf("  %s's public page\r\n", target)
		s.HLine()
		for _, line := range strings.Split(page, "\n") {
			s.Printf("  %s\r\n", line)
		}
		s.HLine()
		return nil
	}

	// Edit own public page
	s.Println("Current public page:")
	if s.User.PublicPage == "" {
		s.Println("  (none)")
	} else {
		printWrapped(s, s.User.PublicPage)
	}
	s.Println("")
	s.Println("Enter new public page (end with '.' on a line by itself):")

	rl := NewReadline(s.r, s.w)
	var lines []string
	for {
		line, err := rl.ReadLine("")
		if err != nil || line == "." {
			break
		}
		lines = append(lines, line)
	}
	page := strings.Join(lines, "\n")
	if err := db.UpdatePublicPage(s.ctx, s.db, s.User.ID, page); err != nil {
		s.Println("error saving public page")
		return nil
	}
	s.User.PublicPage = page
	s.Println("Public page updated.")
	return nil
}
