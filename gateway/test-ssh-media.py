"""Regression checks against the Hermes runtime shipped in the candidate image."""
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from gateway import media_fetch
from gateway.platforms import base
from gateway.session_context import (
    clear_session_vars, get_session_env, set_session_vars,
)
from tools.terminal_scope import reset_terminal_scope, set_terminal_scope


class Remote:
    _remote_home = "/home/agent"

    def __init__(self, content):
        self.content = content

    def fetch_realpath(self, path):
        return path

    def fetch_file(self, path, dest, *, max_bytes):
        Path(dest).write_bytes(self.content)


class SSHMediaSessionTest(unittest.TestCase):
    def setUp(self):
        self.scope = set_terminal_scope({"TERMINAL_ENV": "ssh"})
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(reset_terminal_scope, self.scope)
        self.addCleanup(clear_session_vars, [])
        cache = Path(self.temp.name)
        for name, value in [("DOCUMENT_CACHE_DIR", cache),
                            ("MEDIA_DELIVERY_SAFE_ROOTS", (cache,))]:
            p = patch.object(base, name, value)
            p.start()
            self.addCleanup(p.stop)
        p = patch("gateway.media_policy.media_delivery_strict", return_value=False)
        p.start()
        self.addCleanup(p.stop)

    def test_routes_survive_handler_cleanup_and_do_not_leak(self):
        clear_session_vars([])
        remotes = {"route-a": Remote(b"profile A"), "route-b": Remote(b"profile B")}
        def lookup(task_id):
            # Real terminal lookup derives its session key from the ContextVar.
            return remotes.get(get_session_env("HERMES_SESSION_KEY"))
        with patch("tools.terminal_tool_lifecycle.get_active_env", side_effect=lookup):
            for route, expected in [("route-a", b"profile A"), ("route-b", b"profile B")]:
                result = base.BasePlatformAdapter.filter_media_delivery_paths(
                    [("/remote-only/report.txt", False)], session_key=route)
                self.assertEqual(len(result), 1)
                self.assertEqual(Path(result[0][0]).read_bytes(), expected)
                self.assertEqual(get_session_env("HERMES_SESSION_KEY"), "")

    def test_empty_explicit_key_preserves_existing_context(self):
        set_session_vars(session_key="existing-route")
        with patch.object(media_fetch, "fetch_remote_media", side_effect=lambda path:
                          get_session_env("HERMES_SESSION_KEY")):
            self.assertEqual(base._validated_delivery_path(
                "/remote-only/report.txt", "", "test"), "existing-route")

    def test_strict_policy_still_blocks_remote_fetch(self):
        clear_session_vars([])
        with patch("gateway.media_policy.media_delivery_strict", return_value=True), \
             patch("tools.terminal_tool_lifecycle.get_active_env") as lookup:
            result = base.BasePlatformAdapter.filter_media_delivery_paths(
                [("/remote-only/report.txt", False)], session_key="route-a")
            self.assertEqual(result, [])
            lookup.assert_not_called()

    def test_missing_ssh_cache_reopens_only_scoped_environment(self):
        clear_session_vars([])
        remote = Remote(b"reconnected SSH")
        with patch("tools.terminal_tool_lifecycle.get_active_env", return_value=None), \
             patch("tools.terminal_tool_lifecycle.ensure_task_env", return_value=remote) as reopen:
            result = base.BasePlatformAdapter.filter_media_delivery_paths(
                [("/remote-only/reconnected.txt", False)], session_key="route-reconnect")
            self.assertEqual(len(result), 1)
            self.assertEqual(Path(result[0][0]).read_bytes(), b"reconnected SSH")
            reopen.assert_called_once_with("route-reconnect")


if __name__ == "__main__":
    unittest.main()
