package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/LackOfMorals/charta-query/internal/config"
	"github.com/LackOfMorals/charta-query/internal/skills"
)

// runSkill implements "charta-query skill [--url URL] [--list] [name]": it
// prints a skill as Markdown (SKILL.md format) so an agent can load it
// without a running server.
func runSkill(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("charta-query skill", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: charta-query skill [--url URL] [--list] [name]")
		flags.PrintDefaults()
	}
	url := flags.String("url", config.EnvOr("CHARTA_QUERY_PUBLIC_URL", "http://localhost:7474"), "server base URL written into the examples")
	list := flags.Bool("list", false, "list the available skills")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}
	if *list {
		for _, s := range skills.List() {
			fmt.Fprintf(stdout, "%s\t%s\n", s.Name, s.Description)
		}
		return nil
	}
	name := "charta-query"
	switch flags.NArg() {
	case 0:
	case 1:
		name = flags.Arg(0)
	default:
		return usageError{errors.New("skill takes at most one name")}
	}
	doc, ok := skills.Render(name, *url)
	if !ok {
		return usageError{fmt.Errorf("no skill named %q (see --list)", name)}
	}
	_, err := io.WriteString(stdout, doc)
	return err
}
