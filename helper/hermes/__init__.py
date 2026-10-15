"""/hi-<team> for a hi team's lead (written by hi on every start of the team).

/hi-<team> in the team's Slack channel has hi send the person who typed it their
private links to the app, in which the helper is their chat with the lead
(docs/specs/approved/hi_team_helper_signin.md in hi). No model is asked:
the handler runs hi team web link with the member ID Slack gave the
command, which nobody can type, and the links go only to that person's DM.
"""

import asyncio
import os
import re
import subprocess
import sys

DESCRIPTION = "A private link to the app's chat with the lead"

# Each team's app has a command of its own, /hi-<team>: Slack gives a
# command two apps in a workspace share to the one installed last. /hi
# stays as well, for an app whose manifest still has it.
TEAM = re.sub(r"[^a-z0-9_-]+", "-", os.environ.get("HI_TEAM", "").lower()).strip("-")
COMMANDS = (["hi-" + TEAM] if TEAM else []) + ["hi"]


def _hi(raw_args):
    try:
        from gateway.session_context import get_session_env
    except Exception:  # not the gateway: nobody to send a link to
        return "Type /%s in the team's Slack channel." % COMMANDS[0]
    platform = get_session_env("HERMES_SESSION_PLATFORM")
    user = get_session_env("HERMES_SESSION_USER_ID")
    if platform != "slack" or not user:
        return "Type /%s in the team's Slack channel." % COMMANDS[0]
    return _link(user)


def _link(user):
    """The links for a Slack member, by hi; what to tell them."""
    try:
        done = subprocess.run(["/usr/local/bin/hi", "team", "web", "link", "--user", user],
                              capture_output=True, text=True, timeout=60)
    except Exception as error:
        return "hi couldn't make your link: %s" % error
    out = (done.stdout or "").strip() or (done.stderr or "").strip()
    if out.startswith("hi: "):
        out = out[len("hi: "):]
    return out or "hi couldn't make your link; ask the lead."


def _put_first(original):
    """Slack gets at most 50 slash commands from Hermes, its own first, and
    it has that many, so a plugin's command would get no listener: ours go
    right after /hermes."""
    def slashes():
        # The Slack adapter asks for these as it connects: it is loaded by
        # then, so it can be taught our commands too.
        _answer_in_dms()
        entries = [entry for entry in original() if entry[0] not in COMMANDS]
        return (entries[:1] + [(name, DESCRIPTION, "") for name in COMMANDS] + entries[1:])[:50]
    slashes.hi_team = True
    return slashes


def _answer_in_dms():
    """Hermes drops a slash command typed in a DM when DMs are off, as they
    are for a team's lead, before any plugin sees it; and the bot's DM is
    where people type it. So the Slack adapter's listener answers ours
    itself, wherever they are typed: the person is the user_id Slack sets,
    hi checks they may ask for work, and the links go only to their DM."""
    for module in list(sys.modules.values()):
        adapter = getattr(module, "SlackAdapter", None)
        listener = getattr(adapter, "_handle_hermes_command", None)
        if listener is None or getattr(listener, "hi_team", False):
            continue

        def make(original):
            async def handle(self, ack, command):
                if (command.get("command") or "").lstrip("/") not in COMMANDS:
                    return await original(self, ack, command)
                await ack(response_type="ephemeral", text="Making your links…")
                user = command.get("user_id") or ""
                text = await asyncio.get_running_loop().run_in_executor(None, _link, user) if user else "Slack didn't say who you are."
                url = command.get("response_url")
                if url:
                    import aiohttp
                    async with aiohttp.ClientSession(trust_env=True) as session:  # the box's proxy
                        await session.post(url, json={"response_type": "ephemeral", "replace_original": True, "text": text},
                                           timeout=aiohttp.ClientTimeout(total=15))
            handle.hi_team = True
            return handle

        adapter._handle_hermes_command = make(listener)


def register(ctx):
    for name in COMMANDS:
        ctx.register_command(name, handler=_hi, description=DESCRIPTION)
    try:
        from hermes_cli import commands_platforms
        if not getattr(commands_platforms.slack_native_slashes, "hi_team", False):
            commands_platforms.slack_native_slashes = _put_first(commands_platforms.slack_native_slashes)
    except Exception:
        pass
