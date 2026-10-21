const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');

const repository = 'gox-lang/server';
const targets = ['linux_amd64.tar.gz', 'linux_arm64.tar.gz', 'darwin_amd64.tar.gz', 'darwin_arm64.tar.gz', 'windows_amd64.zip', 'windows_arm64.zip'];

function normalize(value) {
  if (!/^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(value)) throw new Error('Expected a stable version such as v0.1.4');
  return value.replace(/^v/, '');
}

function validate(release, requested, latest) {
  const version = normalize(release.tag_name);
  if (release.tag_name !== 'v' + version || release.draft || release.prerelease) throw new Error('Release must be published and stable');
  if (requested && normalize(requested) !== version) throw new Error('Release version mismatch');
  if (latest && latest.tag_name !== release.tag_name) throw new Error('Refusing to publish a version that is not the latest stable release');
  const names = [...targets.map(target => 'gox_' + version + '_' + target), 'checksums.sha256'];
  for (const name of names) {
    const matches = release.assets.filter(asset => asset.name === name);
    if (matches.length !== 1 || matches[0].state !== 'uploaded' || matches[0].size <= 0 ||
        matches[0].browser_download_url !== 'https://github.com/' + repository + '/releases/download/v' + version + '/' + name) {
      throw new Error('Missing or invalid official asset: ' + name);
    }
  }
  return version;
}

function checksums(text, version) {
  const expected = new Set(targets.map(target => 'gox_' + version + '_' + target));
  const parsed = {};
  for (const line of text.trim().split(/\r?\n/)) {
    const match = /^([a-f0-9]{64})  (\S+)$/.exec(line);
    if (!match || !expected.has(match[2]) || parsed[match[2]]) throw new Error('Invalid checksum manifest');
    parsed[match[2]] = match[1];
  }
  if (Object.keys(parsed).length !== expected.size) throw new Error('Incomplete checksum manifest');
  return parsed;
}

async function get(url, authenticated = false) {
  const headers = { 'User-Agent': 'gox-release-distribution' };
  if (authenticated && process.env.GH_TOKEN) headers.Authorization = 'Bearer ' + process.env.GH_TOKEN;
  const response = await fetch(url, { headers, signal: AbortSignal.timeout(60000) });
  if (!response.ok) throw new Error('HTTP ' + response.status + ' fetching ' + url);
  return response.text();
}

async function main() {
  const requested = process.env.REQUESTED_VERSION || '';
  if (requested) normalize(requested);
  const api = 'https://api.github.com/repos/' + repository + '/releases/';
  const requireLatest = process.env.REQUIRE_LATEST !== 'false';
  const latest = (!requested || requireLatest) ? JSON.parse(await get(api + 'latest', true)) : null;
  const release = requested ? JSON.parse(await get(api + 'tags/v' + normalize(requested), true)) : latest;
  const version = validate(release, requested, requireLatest ? latest : null);
  const manifest = await get('https://github.com/' + repository + '/releases/download/v' + version + '/checksums.sha256');
  checksums(manifest, version);
  const directory = fs.mkdtempSync(path.join(process.env.RUNNER_TEMP || os.tmpdir(), 'gox-release-'));
  const filename = path.join(directory, 'checksums.sha256');
  fs.writeFileSync(filename, manifest);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, 'version=' + version + '\ntag=v' + version + '\nchecksums=' + filename + '\n');
  console.log('Validated GOX v' + version + ' and six official archive checksums.');
}

module.exports = { normalize, validate, checksums, targets };
if (require.main === module) main().catch(error => { console.error(error.message); process.exitCode = 1; });
