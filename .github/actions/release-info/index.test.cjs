const { test } = require('node:test');
const assert = require('node:assert/strict');
const { normalize, validate, checksums, targets } = require('./index.cjs');
const version = '0.1.4';
function release() {
  return { tag_name: 'v' + version, draft: false, prerelease: false, assets: [...targets.map(t => 'gox_' + version + '_' + t), 'checksums.sha256'].map(name => ({
    name, state: 'uploaded', size: 10, browser_download_url: 'https://github.com/gox-lang/server/releases/download/v' + version + '/' + name,
  })) };
}
test('only stable semantic versions accepted', () => {
  assert.equal(normalize('v0.1.4'), version);
  for (const value of ['main', 'v1.0.0-rc1', '01.2.3', '1.2.3\n', '1.2.3;echo bad']) assert.throws(() => normalize(value));
});
test('reject unpublished, incomplete, foreign and stale releases', () => {
  assert.equal(validate(release(), version, release()), version);
  for (const mutate of [r => r.draft = true, r => r.prerelease = true, r => r.assets.pop(), r => r.assets[0].browser_download_url = 'https://example.com/binary', r => r.assets.push(r.assets[0])]) {
    const r = release(); mutate(r); assert.throws(() => validate(r, version, release()));
  }
  assert.throws(() => validate(release(), version, { tag_name: 'v0.1.5' }));
  assert.throws(() => validate(release(), '0.1.3', release()));
});
test('manifest must cover each official archive exactly once', () => {
  const lines = targets.map(t => 'a'.repeat(64) + '  gox_' + version + '_' + t);
  assert.equal(Object.keys(checksums(lines.join('\n'), version)).length, 6);
  assert.throws(() => checksums(lines.slice(1).join('\n'), version));
  assert.throws(() => checksums([...lines, lines[0]].join('\n'), version));
  assert.throws(() => checksums(lines.join('\n').replace('linux_amd64.tar.gz', '../evil'), version));
});
