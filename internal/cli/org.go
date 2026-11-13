package cli

import (
	"fmt"
	"io"
	"strings"
)

// hi server is the organization: devices join and leave it, and its server
// runs on one box. The commands that join (once hi connect and hi
// disconnect) are handled here; everything else is the server's, in
// runOrgServer. hi server, hi connect, and hi disconnect still run these, and
// are never removed: old units and scripts keep running.

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
			fmt.Fprintln(stderr, "usage: hi server join <server> [--user NAME] [--no-wait]")
			return 2
		}
		return exitCode(connectCommand(rest, stdout, stderr), stderr)
	case "status":
		err := connectStatusCommand(rest, stdout)
		if _, set := loadAdminConfig(); set && len(rest) == 0 {
			fmt.Fprintln(stdout)
			if pageErr := adminStatus(stdout); err == nil {
				err = pageErr
			}
		}
		return exitCode(err, stderr)
	// The admin page is the server's (hi_server_apps.md, release 3).
	case "link":
		return exitCode(adminLinkCommand(rest, stdout), stderr)
	case "address":
		return exitCode(adminAddressCommand(rest, stdout), stderr)
	case "mail", "email":
		return exitCode(adminMailCommand(rest, stdin, stdout), stderr)
	case "sites":
		return exitCode(adminSitesCommand(rest, stdout), stderr)
	case "__page":
		return exitCode(adminServe(stderr), stderr)
	case "__job":
		return adminJobMain(rest, stderr)
	case "__askpass":
		return adminAskpassMain(rest, stdout, stderr)
	case "key":
		return exitCode(connectKeyCommand(rest, stdout), stderr)
	case "leave":
		return runDisconnect(rest, stdout, stderr)
	case "slack":
		if len(rest) > 0 && rest[0] == "link" {
			return orgSlackLink(rest[1:], stdout, stderr)
		}
	}
	return runOrgServer(args, stdin, stdout, stderr)
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
	fmt.Fprintln(w, `hi server is the server your machines and people join. It keeps who belongs
and their devices, holds the provider keys, and approves and counts what
compute costs, so nothing paid starts without an approval. It serves your
team's shared keys, private project templates (hi init), and Hugging Face
datasets and models (hi data). Without joining, hi compute uses your own keys.
hi org is its old name and still works.

On any device:
  hi server join <server> [--user NAME] [--no-wait]
                                       Join: enroll this device and wait for an admin's approval
  hi server join <server> --agent NAME --owner PERSON
                                       Enroll an agent that runs on its own, such as a build bot
  hi server status                     Server, user, group, and managed providers
  hi server key                        This device's public key, for an admin to pre-approve
  hi server slack link <code>          Link your Slack account, with the code from /hi link
  hi server leave                      Forget the server and delete this device's key

On the server box:
  hi server init --owner <Slack ID> --email <address> [--listen ADDR] [--local | --netbird F]
                                       Create the server and its owner here, and start it and the admin page
  hi server run                        Serve clients (hi-org.service runs it)
  hi server owner [<Slack ID> --email <address>]
                                       The server's owner, who owns the machine and its apps too; or hand it on
  hi server admins [add <Slack ID> --name <user> [--email E] | remove <user>]
                                       The server's admins: they approve, and run the machine's apps on the admin page
  hi server link [<admin>]             A new sign-in link to the admin page, for 14 days; the old one ends
  hi server address [<host> [--https] | default]
                                       The name the IT team keeps for the admin page; admin.<domain> by default
  hi server mail [set ... | test <address> | off]
                                       How the machine sends email: sign-in links and invitations
  hi server sites [allow <site> [--why W] | remove <site> | org on|off]
                                       What every app's dev box reaches beyond its own list
  hi server provider add|remove <p>    Store or remove a provider key (runpod, shadeform)
  hi server provider list              Show managed providers
  hi server user add <u> --group G [--key K]
                                       Add a user; --key pre-approves a device
  hi server user remove <u>            Remove a user and all their devices
  hi server user list                  Users, groups, and devices
  hi server requests [--all]           Pending requests (--all: every request)
  hi server approve <id> [--group G]   Approve an enrollment or compute request
  hi server deny <id> [--reason R]     Deny a request
  hi server ls                         Everything running, for every user
  hi server stop <name> | --user U | --all
                                       Stop instances
  hi server audit [--since 7d]         Requests, approvals, starts, and stops
  hi server slack setup                Connect a Slack app (see: hi server slack manifest)
  hi server approvers add <Slack ID> --name <user>
                                       Let someone approve and stop from Slack
  hi server approvers remove <Slack ID> | list
  hi server policy show|edit|example|check
                                       Groups: limits, auto-approve, budgets
  hi server spend [--since 30d]        Spend per user and group, compute and models
  hi server ai [set|off|remove]        Pass hi q's model requests to OpenRouter with your team's key,
                                       and to models served on the team's machines (hi serve --org)
  hi server ai remove local/<name>     Stop passing requests to a model served on a team's machine
  hi server templates add <name> <git-url> [--ref R]
                                       Serve a private repository's templates and skills
  hi server templates list|sync|remove    Show, fetch now, or stop serving template sources
  hi server templates rename <name> <new> Rename a source; keeps its token and history
  hi server data                       Hugging Face organizations for hi data (menu)
  hi server data add <hf-org>... [--token-file F]
                                       Serve a Hugging Face organization's datasets, models, and buckets
  hi server data list|test|remove      Show, check, or stop serving organizations
  hi server expose [stop]              The public URL cloud runs reach hi data through, if any

Commands on the server box talk to the running server through its admin
socket. Decisions are recorded under your login name; change it with --as.
The admin page (hi server init) does all of this in a browser, and shows
what runs and what waits; a wall link shows that on a shared screen.

Options:
  --dir <dir>    State directory (default: $HI_ORG_DIR or ~/.local/state/hi/org)`)
}

// oldNameIsTerminal is replaced in tests.
var oldNameIsTerminal = func(w io.Writer) bool {
	r, ok := w.(io.Reader)
	return ok && isTerminal(r)
}

// oldNameHint tells a person at a terminal the new name of hi connect or hi
// disconnect. hi server is the name again and hi org stays, silently, beside
// it (hi_server_apps.md). Scripts and units see nothing new.
func oldNameHint(stderr io.Writer, args []string) {
	if args[0] == "server" || args[0] == "org" || !oldNameIsTerminal(stderr) {
		return
	}
	fmt.Fprintf(stderr, "hi: %s is now %s\n", "hi "+strings.Join(args, " "), orgFormOf(args))
}

// orgFormOf is the hi server command an old command runs.
func orgFormOf(args []string) string {
	rest := args[1:]
	switch args[0] {
	case "disconnect":
		return "hi server leave"
	case "connect":
		if len(rest) == 0 || (rest[0] != "status" && rest[0] != "key" && rest[0] != "slack" && rest[0] != "help") {
			rest = append([]string{"join"}, rest...)
		}
	}
	return strings.TrimSpace("hi server " + strings.Join(rest, " "))
}
