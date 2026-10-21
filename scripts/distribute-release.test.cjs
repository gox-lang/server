const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { main } = require('./distribute-release.cjs');

test('waits for matching publication, propagates failure and scopes dispatch', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'gox-dispatch-test-'));
  Object.assign(process.env, { GH_TOKEN: 'test-only', CHANNEL: 'npm', VERSION: '0.1.4', GITHUB_RUN_ID: '123', GITHUB_RUN_ATTEMPT: '2', GITHUB_STEP_SUMMARY: path.join(directory, 'summary') });
  try {
    for (const conclusion of ['success', 'failure', 'cancelled']) {
      let calls = 0;
      const request = async (url, options) => {
        assert(url.startsWith('https://api.github.com/repos/gox-lang/npm/'));
        calls++;
        if (calls === 1) {
          assert.equal(options.method, 'POST');
          assert.deepEqual(JSON.parse(options.body), { ref: 'main', inputs: { version: '0.1.4', request_id: '123-2' } });
          return { ok: true, status: 204 };
        }
        if (calls === 2) return { ok: true, json: async () => ({ workflow_runs: [
          { id: 1, display_title: 'npm 0.1.4 / another-request', head_branch: 'main' },
          { id: 2, display_title: 'npm 0.1.4 / 123-2', head_branch: 'main', html_url: 'https://github.com/gox-lang/npm/actions/runs/2' },
        ] }) };
        assert(url.endsWith('/actions/runs/2'));
        return { ok: true, json: async () => ({ id: 2, status: 'completed', conclusion }) };
      };
      const promise = main({ request, pause: async () => {} });
      if (conclusion === 'success') await promise;
      else await assert.rejects(promise, new RegExp(conclusion));
      assert.equal(calls, 3);
    }
    process.env.GH_TOKEN = '';
    await assert.rejects(main(), /RELEASE_CHANNELS_TOKEN/);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
