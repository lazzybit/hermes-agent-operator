"""Native ntfy attachment contract, run inside the candidate Hermes image."""
import asyncio
import tempfile
import unittest
from pathlib import Path
from email.header import decode_header, make_header
import httpx
from gateway.config import PlatformConfig
from plugins.platforms.ntfy.adapter import NtfyAdapter, register


class NtfyMediaTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.file = Path(self.temp.name, '黑色.png')
        self.file.write_bytes(b'\x89PNG\r\n\x1a\nsmall-image')
        self.requests = []
        self.response = httpx.Response(200, json={'id': 'native-image', 'attachment': {'name': '黑色.png', 'type': 'image/png', 'url': 'https://push.example/file/image.png'}})
        async def handle(request):
            self.requests.append(request)
            return self.response
        self.client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
        self.addAsyncCleanup(self.client.aclose)
        self.adapter = NtfyAdapter(PlatformConfig(extra={'server': 'https://push.example', 'topic': 'inbox', 'publish_topic': 'outbox', 'token': 'test-token'}))
        self.adapter._http_client = self.client

    async def test_image_is_native_binary_attachment(self):
        result = await self.adapter.send_image('inbox', str(self.file), caption='純黑圖片')
        self.assertTrue(result.success, result.error)
        req = self.requests[-1]
        self.assertEqual(req.method, 'PUT')
        self.assertEqual(req.url.path, '/outbox')
        self.assertEqual(req.content, self.file.read_bytes())
        self.assertEqual(str(make_header(decode_header(req.headers['X-Filename']))), self.file.name)
        self.assertEqual(str(make_header(decode_header(req.headers['X-Message']))), '純黑圖片')
        self.assertEqual(req.headers['X-Tags'], 'hermes-agent')
        self.assertEqual(req.headers['Authorization'], 'Bearer test-token')
        self.assertNotEqual(req.headers['Content-Type'], 'text/plain; charset=utf-8')

    async def test_text_document_forces_attachment_and_metadata_routing(self):
        self.file.write_text('tiny UTF-8 text')
        result = await self.adapter.send_document('inbox', str(self.file), file_name='report.txt', metadata={'publish_topic': 'other'})
        self.assertTrue(result.success, result.error)
        req = self.requests[-1]
        self.assertEqual(req.method, 'PUT')
        self.assertEqual(req.url.path, '/other')
        self.assertEqual(req.headers['X-Filename'], 'report.txt')

    async def test_http_error_is_failure(self):
        self.response = httpx.Response(413, text='too large')
        result = await self.adapter.send_image('inbox', str(self.file))
        self.assertFalse(result.success)

    async def test_success_without_attachment_is_failure(self):
        self.response = httpx.Response(200, json={'id': 'text-only'})
        result = await self.adapter.send_image('inbox', str(self.file))
        self.assertFalse(result.success)

    async def test_missing_file_does_not_publish(self):
        result = await self.adapter.send_document('inbox', str(self.file.parent / 'missing.png'))
        self.assertFalse(result.success)
        self.assertEqual(self.requests, [])

    async def test_platform_hint_teaches_media(self):
        class Context:
            def register_platform(self, **kwargs):
                self.entry = kwargs
        ctx = Context()
        register(ctx)
        self.assertIn('MEDIA:/absolute/path/to/file', ctx.entry['platform_hint'])


if __name__ == '__main__':
    unittest.main()
