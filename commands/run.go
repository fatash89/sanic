package commands

import (
	"fmt"
	"github.com/agnivade/levenshtein"
	"github.com/distributed-containers-inc/sanic/config"
	"github.com/distributed-containers-inc/sanic/shell"
	"github.com/urfave/cli"
	"sort"
	"strings"
)

func runCommandAction(c *cli.Context) error {
	if c.NArg() == 0 {
		return newUsageError(c)
	}

	s, err := shell.Current()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}

	cfg, err := config.Read()
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}

	env, err := cfg.CurrentEnvironment(s)
	if err != nil {
		return cli.NewExitError(err.Error(), 1)
	}

	commandName := c.Args().First()
	var commandNames []string
	for _, command := range env.Commands {
		if command.Name == commandName {
			code, err := s.ShellExec(command.Command, c.Args().Tail())
			if err == nil {
				return nil
			}
			return cli.NewExitError(err.Error(), code)
		}
		commandNames = append(commandNames, command.Name)
	}
	sort.Slice(commandNames, func(i, j int) bool {
		distI := levenshtein.ComputeDistance(commandNames[i], commandName)
		distJ := levenshtein.ComputeDistance(commandNames[j], commandName)
		return distI < distJ
	})
	if len(commandNames) > 6 {
		commandNames = commandNames[:6]
	}
	return cli.NewExitError(
		fmt.Sprintf("Command %s was not found in environment %s. Did you mean one of [%s]?",
			commandName,
			s.GetSanicEnvironment(),
			strings.Join(commandNames, "|"),
		), 1)

}

var runCommand = cli.Command{
	Name:            "run",
	Usage:           "run a configured script in the configuration",
	Action:          runCommandAction,
	SkipArgReorder:  true,
	SkipFlagParsing: true,
}
