"""/hi-<app> for a hi app's lead (written by hi on every start of the app),
and Allow and Say no on hi's posts that the dev box asks for a site.

/hi-<app> in the app's Slack channel has hi send an owner who typed it their
private link to the app's dev page (docs/specs/approved/hi_app_dev.md in
hi); anyone else hears that the app's own link is all they need. No model is asked:
the handler runs hi app web link with the member ID Slack gave the
command, which nobody can type, and the links go only to that person's DM.
"""

import asyncio
import os
import re
import subprocess
import sys
import time

DESCRIPTION = "Owners: your link to the dev page; signin chatgpt|claude signs the app in again"

# Each app has a command of its own, /hi-<app>: Slack gives a
# command two apps in a workspace share to the one installed last. Plain
# /hi is the server's: answering it too would let an app whose
# manifest still has it take /hi from the server when reinstalled.
TEAM = re.sub(r"[^a-z0-9_-]+", "-", os.environ.get("HI_TEAM", "").lower()).strip("-")
COMMANDS = ["hi-" + TEAM] if TEAM else []
TYPE = "Type /%s in the app's Slack channel." % (COMMANDS[0] if COMMANDS else "hi-<app>")


def _hi(raw_args):
    try:
        from gateway.session_context import get_session_env
    except Exception:  # not the gateway: nobody to send a link to
        return TYPE
    platform = get_session_env("HERMES_SESSION_PLATFORM")
    user = get_session_env("HERMES_SESSION_USER_ID")
    if platform != "slack" or not user:
        return TYPE
    return _answer(user, raw_args)


def _answer(user, text):
    """What /hi-<app> does: the person's links, or, as "signin chatgpt" or
    "signin claude", an owner's sign-in, sent to them alone in a DM."""
    words = (text or "").split()
    if words and words[0] == "signin":
        if len(words) != 2:
            return "Sign the app in again with /%s signin chatgpt or /%s signin claude." % (COMMANDS[0], COMMANDS[0]) if COMMANDS else "signin chatgpt or signin claude"
        return _run(["app", "web", "signin", words[1], "--user", user], "your sign-in")
    if words and words[0] == "dev":
        return _run(["app", "web", "link", "--user", user, "--dev"], "your link to the dev page")
    return _link(user)


def _link(user):
    """The links for a Slack member, by hi; what to tell them."""
    return _run(["app", "web", "link", "--user", user], "your link")


def _run(args, what):
    """hi in the box, which asks the app's service; what to tell them."""
    try:
        done = subprocess.run(["/usr/local/bin/hi"] + args, capture_output=True, text=True, timeout=60)
    except Exception as error:
        return "hi couldn't make %s: %s" % (what, error)
    out = (done.stdout or "").strip() or (done.stderr or "").strip()
    if out.startswith("hi: "):
        out = out[len("hi: "):]
    return out or "hi couldn't make %s; ask the lead." % what


def _site_button(answer):
    """Allow or Say no on hi's post that the dev box asks for a site, or Keep
    or Undo on its post of what its check allowed, in the ops channel: hi
    checks the one who clicked (the user Slack gives the click) is an
    owner, and the button's value carries the ask's key. hi then puts the
    answer in place of the buttons; anything else, such as someone who
    isn't an owner, hears why, alone."""
    async def handle(ack, body, action):
        await ack()
        user = ((body or {}).get("user") or {}).get("id") or ""
        value = (action or {}).get("value") or ""
        if not user or not value:
            return
        args = ["app", "web", "site", value, answer, "--user", user]
        text = await asyncio.get_running_loop().run_in_executor(None, _run, args, "the answer")
        url = (body or {}).get("response_url")
        if url and not text.startswith(("Allowed ", "Said no ", "Kept ", "Undid ")):
            import aiohttp
            async with aiohttp.ClientSession(trust_env=True) as session:  # the box's proxy
                await session.post(url, json={"response_type": "ephemeral", "replace_original": False, "text": text},
                                   timeout=aiohttp.ClientTimeout(total=15))
    return handle


_MAY = {}  # member ID -> (may they, until when)


def _may_ask(user):
    """Whether someone may ask the team for work, so the lead answers their
    DM: hi decides, as for /hi-<app>. Kept a few minutes."""
    known = _MAY.get(user)
    if known and known[1] > time.monotonic():
        return known[0]
    try:
        # A few seconds at most: this runs in Hermes' synchronous check, which
        # may be on its event loop, and every Slack event would wait.
        done = subprocess.run(["/usr/local/bin/hi", "app", "web", "may", "--user", user],
                              capture_output=True, text=True, timeout=5)
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
    slashes.hi_app = True
    return slashes


def _answer_in_dms():
    """Hermes drops a slash command typed in a DM when DMs are off, as they
    are for an app's lead, before any plugin sees it; and the bot's DM is
    where people type it. So the Slack adapter's listener answers ours
    itself, wherever they are typed: the person is the user_id Slack sets,
    hi checks they may ask for work, and the links go only to their DM."""
    for module in list(sys.modules.values()):
        adapter = getattr(module, "SlackAdapter", None)
        listener = getattr(adapter, "_handle_hermes_command", None)
        if listener is None or getattr(listener, "hi_app", False):
            continue

        def make(original):
            async def handle(self, ack, command):
                if (command.get("command") or "").lstrip("/") not in COMMANDS:
                    return await original(self, ack, command)
                await ack(response_type="ephemeral", text="One moment…")
                user = command.get("user_id") or ""
                text = await asyncio.get_running_loop().run_in_executor(None, _answer, user, command.get("text") or "") if user else "Slack didn't say who you are."
                url = command.get("response_url")
                if url:
                    import aiohttp
                    async with aiohttp.ClientSession(trust_env=True) as session:  # the box's proxy
                        await session.post(url, json={"response_type": "ephemeral", "replace_original": True, "text": text},
                                           timeout=aiohttp.ClientTimeout(total=15))
            handle.hi_app = True
            return handle

        adapter._handle_hermes_command = make(listener)


# The lead answers and files tasks; the owners' agents read the code.
# Past a dozen reads and searches in one turn, each result says so
# (docs/specs/approved/hi_app_lead_steps.md in hi). Written for
# google/gemini-3.8-flash, whose leads on 2026-10-20 spent whole 60-step
# turns mostly in search_files over /team/repo/src: re-check the counts on
# another model before keeping it.
READS = {"search_files", "read_file"}
READS_BEFORE_NOTE = 12
READS_NOTE = ("\n\n[hi: that is %d reads of the code in this turn. You have enough to answer: put "
              "what you found in the task (hi app task comment) or on the incident (hi app "
              "incidents note <id> \"...\"), then answer. The owners' agents read the code properly "
              "in the dev box.]")
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
    _quiet_group_dms()
    register_action = getattr(ctx, "register_slack_action_handler", None)
    if register_action:  # a Hermes without it: the dev page's buttons still answer
        register_action("hi_site_allow", _site_button("allow"))
        register_action("hi_site_deny", _site_button("no"))
        register_action("hi_site_keep", _site_button("keep"))
        register_action("hi_site_undo", _site_button("undo"))
    try:
        ctx.register_hook("transform_tool_result", _note_reads)
    except Exception:  # a Hermes without the hook: the budget still stops the turn
        pass
    try:
        from hermes_cli import commands_platforms
        if not getattr(commands_platforms.slack_native_slashes, "hi_app", False):
            commands_platforms.slack_native_slashes = _put_first(commands_platforms.slack_native_slashes)
    except Exception:
        pass


GROUP_DM_WARNING = "[Slack] Group DMs (multi-person DMs) will not work"


def _quiet_group_dms():
    """An app's Slack app asks for no group-DM scopes on purpose: people
    talk to the lead in the channel or one to one. Hermes 0.23 warns at
    every start that group DMs won't work; that one line goes."""
    import logging

    def keep(record):
        return not str(record.msg).startswith(GROUP_DM_WARNING)

    for name in ("hermes_plugins.platforms__slack.adapter", "plugins.platforms.slack.adapter"):
        logger = logging.getLogger(name)
        if not any(getattr(f, "hi_app", False) for f in logger.filters):
            keep.hi_app = True
            logger.addFilter(keep)


def _guard_dms():
    """The lead answers DMs from the people who may ask for work only:
    Hermes' own gate lets the whole workspace in when the app has no
    askers. Anyone else's DM is dropped before Hermes reads it."""
    import logging
    log = logging.getLogger("hi-team")
    for module in list(sys.modules.values()):
        adapter = getattr(module, "SlackAdapter", None)
        check = getattr(adapter, "_early_reject_unauthorized", None)
        if check is None or getattr(check, "hi_app", False):
            continue

        def make(original):
            # Hermes 0.23 passes thread_id too; what comes after is_dm
            # goes on as it came.
            def reject(self, user_id, channel_id, is_dm, *args, **kwargs):
                if is_dm and not (user_id and _may_ask(user_id)):
                    log.info("hi-team: dropped a DM from %s, who may not ask the team for work", user_id)
                    return True
                return original(self, user_id, channel_id, is_dm, *args, **kwargs)
            reject.hi_app = True
            return reject

        adapter._early_reject_unauthorized = make(check)
