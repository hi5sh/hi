"""A first reply to a new message in a DM with a hi team's lead (written by
hi on every start of the team).

Slack shows the lead's status ("is working") only in a thread, and a
message at the top of a DM has no thread until the answer comes, which may
take minutes. So when the lead starts on one, hi posts "Looking into it…"
in its thread at once, and deletes it once the lead has answered. A reply
in a thread needs none: the status shows there already. No model is asked.
"""

import asyncio
import json
import os
import urllib.parse
import urllib.request

ACK = "Looking into it…"

# The session's first reply, to delete at its end: session id → (channel, ts).
_acks = {}


def _slack(method, **args):
    token = os.environ.get("SLACK_BOT_TOKEN", "")
    if not token:
        return {}
    request = urllib.request.Request(
        "https://slack.com/api/" + method,
        data=urllib.parse.urlencode(args).encode(),
        headers={"Authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.load(response)


def _new_message(channel, user):
    """The person's newest message at the top of the DM, when nobody has
    answered it yet: the one the lead starts on."""
    history = _slack("conversations.history", channel=channel, limit=5)
    for message in history.get("messages", []):
        if message.get("user") != user or message.get("subtype"):
            continue
        if message.get("reply_count") or message.get("thread_ts", message.get("ts")) != message.get("ts"):
            return ""
        return message.get("ts", "")
    return ""


def _start(context):
    channel = context.get("chat_id", "")
    if context.get("chat_type") != "dm" or context.get("thread_id") or not channel:
        return
    ts = _new_message(channel, context.get("user_id", ""))
    if not ts:
        return
    posted = _slack("chat.postMessage", channel=channel, thread_ts=ts, text=ACK)
    if posted.get("ok"):
        _acks[context.get("session_id", "")] = (channel, posted["ts"])


def _end(context):
    channel, ts = _acks.pop(context.get("session_id", ""), ("", ""))
    if ts:
        _slack("chat.delete", channel=channel, ts=ts)


async def handle(event_type, context):
    if context.get("platform") != "slack":
        return
    try:
        if event_type == "agent:start":
            await asyncio.to_thread(_start, context)
        elif event_type == "agent:end":
            await asyncio.to_thread(_end, context)
    except Exception as error:  # a hook never stops the lead
        print(f"[hi-dm-ack] {error}", flush=True)
