package shell

import (
	"fmt"
	"strings"
)

// CommandFunc is the signature every command handler must implement.
type CommandFunc func(s *Session, args []string) error

var registry = map[string]CommandFunc{}
var aliases = map[string]string{}

func register(name string, fn CommandFunc, aliasList ...string) {
	registry[name] = fn
	for _, a := range aliasList {
		aliases[a] = name
	}
}

func dispatch(s *Session, line string) error {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}
	name := strings.ToLower(parts[0])
	args := parts[1:]

	if canonical, ok := aliases[name]; ok {
		name = canonical
	}

	fn, ok := registry[name]
	if !ok {
		s.Printf("command not found: %s  (type 'help' for a list)\r\n", name)
		return nil
	}
	s.hub.Touch(s.ID)
	s.SetState(name)
	defer s.SetState("shell")
	return fn(s, args)
}

func commandList() []string {
	seen := map[string]bool{}
	var names []string
	for name := range registry {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func usageError(s *Session, cmd, usage string) {
	s.Printf("usage: %s %s\r\n", cmd, usage)
}

func init() {
	// Register all commands. Handlers are defined in cmd_*.go files.
	register("finger", cmdFinger)
	register("who", cmdWho)
	register("rwho", cmdRwho)
	register("w", cmdW)
	register("last", cmdLast)
	register("write", cmdWrite)
	register("talk", cmdTalk, "ytalk")
	register("call", cmdCall)
	register("mesg", cmdMesg)
	register("motd", cmdMotd)
	register("msgs", cmdMsgs)
	register("fortune", cmdFortune)
	register("plan", cmdPlan)
	register("project", cmdProject)
	register("public", cmdPublic)
	register("passwd", cmdPasswd)
	register("chfn", cmdChfn)
	register("mail", cmdMail, "m")
	register("biff", cmdBiff)
	register("vacation", cmdVacation)
	register("news", cmdNews, "rn", "nn")
	register("post", cmdPost)
	register("calendar", cmdCalendar, "cal")
	register("help", cmdHelp, "?")
	register("clear", cmdClear, "cls")
	register("uptime", cmdUptime)
	register("logout", cmdLogout, "exit", "quit", "bye")
	register("wall", cmdWall)
	register("node", cmdNode)
	registerGames()
}

// printColumns formats items into n columns of equal width.
func printColumns(s *Session, items []string, cols int) {
	if len(items) == 0 {
		return
	}
	colW := 0
	for _, item := range items {
		if len(item) > colW {
			colW = len(item)
		}
	}
	colW += 2

	for i, item := range items {
		fmt.Fprintf(s, "%-*s", colW, item)
		if (i+1)%cols == 0 {
			s.Write([]byte("\r\n"))
		}
	}
	if len(items)%cols != 0 {
		s.Write([]byte("\r\n"))
	}
}

