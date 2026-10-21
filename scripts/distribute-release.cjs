// Dispatch only trusted main-branch workflows; wait for their actual conclusion.
const fs = require('node:fs');
const mapping = {
  npm: ['npm', 'release.yml', 'npm'],
  homebrew: ['homebrew-tap', 'release.yml', 'Homebrew'],
  linux: ['linux-packages', 'publish.yml', 'Linux'],
  docker: ['server', 'container-publish.yml', 'Docker'],
};
async function main({ request = fetch, pause = ms => new Promise(resolve => setTimeout(resolve, ms)) } = {}) {
  const { GH_TOKEN: token, CHANNEL: channel, VERSION: version } = process.env;
  if (!token) throw new Error('Configure RELEASE_CHANNELS_TOKEN with Actions read/write on npm, homebrew-tap and linux-packages.');
  if (!mapping[channel] || !/^\d+\.\d+\.\d+$/.test(version)) throw new Error('Invalid release request');
  const [repository, workflow, label] = mapping[channel];
  const requestID = process.env.GITHUB_RUN_ID + '-' + process.env.GITHUB_RUN_ATTEMPT;
  const title = label + ' ' + version + ' / ' + requestID;
  const base = 'https://api.github.com/repos/gox-lang/' + repository;
  async function api(endpoint, body) {
    const response = await request(base + endpoint, {
      method: body ? 'POST' : 'GET',
      headers: { Authorization: 'Bearer ' + token, Accept: 'application/vnd.github+json', 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.timeout(30000),
    });
    if (!response.ok) throw new Error('GitHub HTTP ' + response.status + ' for ' + repository + endpoint);
    return response.status === 204 ? null : response.json();
  }
  const inputs = { version, request_id: requestID };
  if (channel === 'docker') inputs.latest = 'true';
  const since = new Date(Date.now() - 60000).toISOString();
  await api('/actions/workflows/' + workflow + '/dispatches', { ref: 'main', inputs });
  const deadline = Date.now() + 225 * 60 * 1000;
  let run;
  while (Date.now() < deadline) {
    if (!run) {
      const result = await api('/actions/workflows/' + workflow + '/runs?event=workflow_dispatch&per_page=100&created=' + encodeURIComponent('>=' + since));
      run = result.workflow_runs.find(candidate => candidate.display_title === title && candidate.head_branch === 'main');
      if (run) {
        console.log(run.html_url);
        fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, '- [' + channel + ' ' + version + '](' + run.html_url + ')\n');
      }
    } else {
      run = await api('/actions/runs/' + run.id);
      if (run.status === 'completed') {
        fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, '  Result: **' + run.conclusion + '**\n');
        if (run.conclusion !== 'success') throw new Error(channel + ' publication: ' + run.conclusion);
        return;
      }
    }
    await pause(30000);
  }
  throw new Error('Timed out waiting for ' + channel + '; inspect the linked run before retrying.');
}
module.exports = { main };
if (require.main === module) main().catch(error => { console.error(error.message); process.exitCode = 1; });
