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
import time

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


_MAY = {}  # member ID -> (may they, until when)


def _may_ask(user):
    """Whether someone may ask the team for work, so the lead answers their
    DM: hi decides, as for /hi-<team>. Kept a few minutes."""
    known = _MAY.get(user)
    if known and known[1] > time.monotonic():
        return known[0]
    try:
        done = subprocess.run(["/usr/local/bin/hi", "team", "web", "may", "--user", user],
                              capture_output=True, text=True, timeout=30)
        may = done.returncode == 0
    except Exception:
        return False  # not now; the next message asks again
    _MAY[user] = (may, time.monotonic() + (600 if may else 60))
    return may


def _put_first(original):
    """Slack gets at most 50 slash commands from Hermes, its own first, and
    it has that many, so a plugin's command would get no listener: ours go
    right after /hermes."""
    def slashes():
        # The Slack adapter asks for these as it connects: it is loaded by
        # then, so it can be taught our commands, and who may DM the lead.
        _answer_in_dms()
        _guard_dms()
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


# The lead talks and hands work on; the member reads the code in its task.
# Past a dozen reads and searches in one turn, each result says so
# (docs/specs/approved/hi_team_lead_steps.md in hi). Written for
# google/gemini-3.8-flash, whose leads on 2026-10-20 spent whole 60-step
# turns mostly in search_files over /team/repo/src: re-check the counts on
# another model before keeping it.
READS = {"search_files", "read_file"}
READS_BEFORE_NOTE = 12
READS_NOTE = ("\n\n[hi: that is %d reads of the code in this turn. The member reads the code in its task: "
              "write what you found where the next turn looks (the brief in hi team task, or "
              "hi team incidents note <id> \"...\"), then answer.]")
_reads = {}  # turn -> reads so far, for the newest turns only


def _note_reads(tool_name="", result=None, turn_id="", session_id="", **_):
    if tool_name not in READS or not isinstance(result, str):
        return None
    turn = turn_id or session_id
    if not turn:
        return None
    count = _reads.pop(turn, 0) + 1
    _reads[turn] = count
    while len(_reads) > 64:
        _reads.pop(next(iter(_reads)))
    if count <= READS_BEFORE_NOTE:
        return None
    return result + READS_NOTE % count


def register(ctx):
    for name in COMMANDS:
        ctx.register_command(name, handler=_hi, description=DESCRIPTION)
    try:
        ctx.register_hook("transform_tool_result", _note_reads)
    except Exception:  # a Hermes without the hook: the budget still stops the turn
        pass
    try:
        from hermes_cli import commands_platforms
        if not getattr(commands_platforms.slack_native_slashes, "hi_team", False):
            commands_platforms.slack_native_slashes = _put_first(commands_platforms.slack_native_slashes)
    except Exception:
        pass


def _guard_dms():
    """The lead answers DMs from the people who may ask for work only:
    Hermes' own gate lets the whole workspace in when the team has no
    askers. Anyone else's DM is dropped before Hermes reads it."""
    import logging
    log = logging.getLogger("hi-team")
    for module in list(sys.modules.values()):
        adapter = getattr(module, "SlackAdapter", None)
        check = getattr(adapter, "_early_reject_unauthorized", None)
        if check is None or getattr(check, "hi_team", False):
            continue

        def make(original):
            def reject(self, user_id, channel_id, is_dm):
                if is_dm and not (user_id and _may_ask(user_id)):
                    log.info("hi-team: dropped a DM from %s, who may not ask the team for work", user_id)
                    return True
                return original(self, user_id, channel_id, is_dm)
            reject.hi_team = True
            return reject

        adapter._early_reject_unauthorized = make(check)
