"""MTProto provisioning helper. JSON input/output is a private credential pipe.

Never logs request values or raw exceptions. Recover by resolving the persisted
username; accept existing bots only when owned by this session and managed by
this exact manager. No interactive login is attempted in the controller.
"""
import asyncio
import json
import logging
import sys
from telethon import TelegramClient, functions
from telethon.errors import UsernameNotOccupiedError
from telethon.sessions import StringSession

logging.disable(logging.CRITICAL)


async def provision(data):
    client = TelegramClient(StringSession(data["session"]), int(data["api_id"]), data["api_hash"])
    await client.connect()
    try:
        if not await client.is_user_authorized():
            raise ValueError("UserSessionUnauthorized")
        owner = await client.get_me()
        if owner.bot:
            raise ValueError("UserSessionRequired")
        manager = await client.get_entity(data["manager_username"])
        if not getattr(manager, "bot_can_manage_bots", False):
            raise ValueError("ManagerPermissionMissing")
        username = data["username"]
        try:
            existing = await client.get_entity(username)
        except UsernameNotOccupiedError:
            existing = None
        except ValueError as exc:
            if not isinstance(exc.__cause__, UsernameNotOccupiedError):
                raise
            existing = None
        if existing is None:
            bot = await client(functions.bots.CreateBotRequest(
                name=data["display_name"], username=username, manager_id=manager))
        else:
            bot = existing
        full = await client(functions.users.GetFullUserRequest(bot))
        if not bot.bot or getattr(full.full_user, "bot_manager_id", None) != manager.id:
            raise ValueError("ExternalIdentityConflict")
        owned = await client(functions.bots.GetAdminedBotsRequest())
        if bot.id not in {item.id for item in owned}:
            raise ValueError("ExternalIdentityUnverified")
        return {"bot_id": bot.id, "username": bot.username,
                "owner_user_id": owner.id, "manager_id": manager.id}
    finally:
        await client.disconnect()


if __name__ == "__main__":
    try:
        result = asyncio.run(provision(json.load(sys.stdin)))
        print(json.dumps(result))
    except Exception as exc:
        print(json.dumps({"error": type(exc).__name__}))
        sys.exit(1)
