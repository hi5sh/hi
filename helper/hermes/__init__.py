"""/hi for a hi team's lead (written by hi on every start of the team).

/hi in the team's Slack channel has hi send the person who typed it their
private links to the app, in which the helper is their chat with the lead
(docs/specs/approved/hi_team_helper_signin.md in hi). No model is asked:
the handler runs hi team web link with the member ID Slack gave the
command, which nobody can type, and the links go only to that person's DM.
"""

import subprocess

DESCRIPTION = "A private link to the app's chat with the lead"


def _hi(raw_args):
    try:
        from gateway.session_context import get_session_env
    except Exception:  # not the gateway: nobody to send a link to
        return "Type /hi in the team's Slack channel."
    platform = get_session_env("HERMES_SESSION_PLATFORM")
    user = get_session_env("HERMES_SESSION_USER_ID")
    if platform != "slack" or not user:
        return "Type /hi in the team's Slack channel."
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
    it has that many, so a plugin's command would get no listener: /hi goes
    right after /hermes."""
    def slashes():
        entries = [entry for entry in original() if entry[0] != "hi"]
        return (entries[:1] + [("hi", DESCRIPTION, "")] + entries[1:])[:50]
    slashes.hi_team = True
    return slashes


def register(ctx):
    ctx.register_command("hi", handler=_hi, description=DESCRIPTION)
    try:
        from hermes_cli import commands_platforms
        if not getattr(commands_platforms.slack_native_slashes, "hi_team", False):
            commands_platforms.slack_native_slashes = _put_first(commands_platforms.slack_native_slashes)
    except Exception:
        pass
