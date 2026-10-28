import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { test } from 'node:test';
import { fixtureOrigin, fixtureURL, fetchFixture } from './fixture-network.mjs';

test('fixture URLs accept only canonical unprivileged literal loopback origins', () => {
  assert.equal(fixtureURL('http://127.0.0.1:36827', '/assets/index-a.js'), 'http://127.0.0.1:36827/assets/index-a.js');
  for (const origin of ['https://example.com:36827', 'http://localhost:36827', 'http://127.0.0.1.example.com:36827', 'http://127.0.0.1:80', 'http://127.0.0.1:65536', 'http://127.0.0.1:036827', 'http://user@127.0.0.1:36827', 'http://127.0.0.1:36827/path', 'http://127.0.0.1:36827?next=x', null]) {
    assert.throws(() => fixtureOrigin(origin));
  }
  for (const path of ['https://example.com/', '//example.com/a.js', '/assets/../secret', '/assets/%2e%2e/secret', '/assets/a.js?secret=x', '/assets/a.js#x', '/assets\\a.js', null]) {
    assert.throws(() => fixtureURL('http://127.0.0.1:36827', path));
  }
});

test('fixture requests refuse redirects without contacting their destination', async () => {
  let destinationHits = 0;
  const server = createServer((request, response) => {
    if (request.url === '/healthz') {
      response.writeHead(302, { Location: '/destination' });
    } else {
      destinationHits++;
      response.writeHead(200);
    }
    response.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  try {
    await assert.rejects(fetchFixture(`http://127.0.0.1:${server.address().port}`, '/healthz'));
    assert.equal(destinationHits, 0);
  } finally {
    await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
});
