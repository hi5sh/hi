package main

import (
	"fmt"
	"io"
)

// hi org is the organization: devices join and leave it, and its server
// runs on one box. The commands that join (once hi connect and hi
// disconnect) are handled here; everything else is the server's, in
// runServer. hi server, hi connect, and hi disconnect still run these.

func runOrg(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printOrgUsage(stdout)
		return 0
	}
	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		printOrgUsage(stdout)
		return 0
	case "join":
		if len(rest) == 0 {
			fmt.Fprintln(stderr, "usage: hi org join <server> [--user NAME] [--no-wait]")
			return 2
		}
		return exitCode(connectCommand(rest, stdout, stderr), stderr)
	case "status":
		return exitCode(connectStatusCommand(rest, stdout), stderr)
	case "key":
		return exitCode(connectKeyCommand(rest, stdout), stderr)
	case "leave":
		return runDisconnect(rest, stdout, stderr)
	case "slack":
		if len(rest) > 0 && rest[0] == "link" {
			return orgSlackLink(rest[1:], stdout, stderr)
		}
	}
	return runServer(args, stdin, stdout, stderr)
}

// orgSlackLink links this device's user to their Slack account, with the
// code /hi link gave them.
func orgSlackLink(args []string, stdout, stderr io.Writer) int {
	if err := connectSlackCommand(args); err != nil {
		return exitCode(err, stderr)
	}
	fmt.Fprintln(stdout, "Linked. Slack now sends you messages about your requests, and its Home tab shows your machines.")
	return 0
}

func printOrgUsage(w io.Writer) {
	fmt.Fprintln(w, `hi org is your organization: its people, devices, and rules. Its server
holds the provider keys and approves compute, so nothing paid starts without
an approval. It also serves your team's private project templates to hi init,
and your team's Hugging Face datasets and models to hi data. Without joining,
hi compute uses your own keys.

On any device:
  hi org join <server> [--user NAME] [--no-wait]
                                       Join: enroll this device and wait for an admin's approval
  hi org join <server> --agent NAME --owner PERSON
                                       Enroll an agent that runs on its own, such as a build bot
  hi org status                        Server, user, group, and managed providers
  hi org key                           This device's public key, for an admin to pre-approve
  hi org slack link <code>             Link your Slack account, with the code from /hi link
  hi org leave                         Forget the server and delete this device's key
  hi org live                          Live dashboard, on a device added as a viewer

On the server box:
  hi org init [--listen ADDR]          Create the server's state here
  hi org run                           Serve clients (run it as a systemd service)
  hi org provider add|remove <p>       Store or remove a provider key (runpod, shadeform)
  hi org provider list                 Show managed providers
  hi org user add <u> --group G [--key K]
                                       Add a user; --key pre-approves a device
  hi org user remove <u>               Remove a user and all their devices
  hi org user list                     Users, groups, and devices
  hi org requests [--all]              Pending requests (--all: every request)
  hi org approve <id> [--group G]      Approve an enrollment or compute request
  hi org deny <id> [--reason R]        Deny a request
  hi org ls                            Everything running, for every user
  hi org stop <name> | --user U | --all
                                       Stop instances
  hi org audit [--since 7d]            Requests, approvals, starts, and stops
  hi org slack setup                   Connect a Slack app (see: hi org slack manifest)
  hi org approvers add <Slack ID> --name <user>
                                       Let someone approve and stop from Slack
  hi org approvers remove <Slack ID> | list
  hi org policy show|edit|example|check
                                       Groups: limits, auto-approve, budgets
  hi org spend [--since 30d]           Spend per user and group, compute and models
  hi org ai [set|off|remove]           Pass hi q's model requests to OpenRouter with your team's key
  hi org live [--wall]                 Live dashboard; --wall is read-only for a shared screen
  hi org wall setup|add|remove|list    Show the wall dashboard on screens over SSH (needs sudo)
  hi org viewer add <name> --key K     A device that may only watch the dashboard
  hi org templates add <name> <git-url> [--ref R]
                                       Serve a private repository's templates and skills
  hi org templates list|sync|remove    Show, fetch now, or stop serving template sources
  hi org templates rename <name> <new> Rename a source; keeps its token and history
  hi org data                          Hugging Face organizations for hi data (menu)
  hi org data add <hf-org>... [--token-file F]
                                       Serve a Hugging Face organization's datasets, models, and buckets
  hi org data list|test|remove         Show, check, or stop serving organizations
  hi org expose [stop]                 The public URL cloud runs reach hi data through, if any

Commands on the server box talk to the running server through its admin
socket. Decisions are recorded under your login name; change it with --as.

Options:
  --dir <dir>    State directory (default: $HI_SERVER_DIR or ~/.local/state/hi/server)`)
}
