import asyncio
import unittest
from types import SimpleNamespace as N
from unittest.mock import patch
import telegram_provision as target


class FakeClient:
    authorized = True
    owned = True
    missing = False
    creates = 0
    disconnected = False
    def __init__(self, *args): pass
    async def connect(self): pass
    async def disconnect(self): self.disconnected = True
    async def is_user_authorized(self): return self.authorized
    async def get_me(self): return N(id=1, bot=False)
    async def get_entity(self, name):
        if name == 'manager_bot': return N(id=2, bot_can_manage_bots=True)
        if self.missing:
            try:
                raise target.UsernameNotOccupiedError(request=None)
            except target.UsernameNotOccupiedError as exc:
                raise ValueError('username does not exist') from exc
        return N(id=3, bot=True, username=name)
    async def __call__(self, req):
        if isinstance(req, target.functions.users.GetFullUserRequest):
            return N(full_user=N(bot_manager_id=2))
        if isinstance(req, target.functions.bots.GetAdminedBotsRequest):
            return [N(id=3)] if self.owned else []
        if isinstance(req, target.functions.bots.CreateBotRequest):
            FakeClient.creates += 1
            return N(id=3, bot=True, username=req.username)
        raise AssertionError('unexpected request')


class ProvisionTests(unittest.TestCase):
    def run_helper(self):
        with patch.object(target, 'TelegramClient', FakeClient), patch.object(target, 'StringSession', lambda x: x):
            return asyncio.run(target.provision(dict(session='fixture', api_id='1', api_hash='fixture', manager_username='manager_bot', username='example_bot', display_name='Example')))
    def test_recovery_requires_real_ownership(self):
        FakeClient.owned = False
        with self.assertRaisesRegex(ValueError, 'ExternalIdentityUnverified'): self.run_helper()
        FakeClient.owned = True
    def test_recovery_returns_verified_identity(self):
        self.assertEqual(self.run_helper()['bot_id'], 3)
    def test_new_bot_creation(self):
        FakeClient.missing = True
        FakeClient.creates = 0
        try:
            self.assertEqual(self.run_helper()['bot_id'], 3)
            self.assertEqual(FakeClient.creates, 1)
        finally: FakeClient.missing = False
    def test_no_interactive_login(self):
        FakeClient.authorized = False
        with self.assertRaisesRegex(ValueError, 'UserSessionUnauthorized'): self.run_helper()
        FakeClient.authorized = True

if __name__ == '__main__': unittest.main()
