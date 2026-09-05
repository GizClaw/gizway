import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const workflow = readFileSync(new URL('../../.github/workflows/review-with-ci.yml', import.meta.url), 'utf8');
const script = workflow.split('          script: |\n')[1].split('\n  review:')[0]
  .split('\n').map(line => line.slice(12)).join('\n');
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const collect = new AsyncFunction('github', 'context', 'core', 'process', script);
const successRun = { id: 3, head_sha: 'head', event: 'pull_request',
  status: 'completed', conclusion: 'success', run_attempt: 1 };
const requiredJobs = ['GizWay SDK gate', 'Full Go and API gate', 'Split-service Compose E2E']
  .map(name => ({ name, status: 'completed', conclusion: 'success' }));

async function probe({ runs = [successRun], jobs = requiredJobs, failure = false } = {}) {
  let output;
  const github = {
    rest: {
      pulls: { get: async () => ({ data: { head: { sha: 'head' } } }) },
      actions: {
        listWorkflowRuns: async args => {
          assert.equal(args.workflow_id, 'ci.yml');
          assert.equal(args.head_sha, 'head');
          if (failure) throw new Error('GitHub unavailable');
          return { data: { workflow_runs: runs } };
        },
        listJobsForWorkflowRun: () => {},
      },
    },
    paginate: async (_method, args) => {
      assert.equal(args.filter, 'latest');
      return jobs;
    },
  };
  await collect(github, { repo: { owner: 'GizClaw', repo: 'gizway' },
    payload: { pull_request: { number: 29 } } },
  { setOutput: (key, value) => { assert.equal(key, 'ci'); output = JSON.parse(value); } },
  { env: {} });
  return output;
}

test('accepts all successful jobs on the exact head', async () => {
  const result = await probe();
  assert.equal(result.all_passed, true);
  assert.equal(result.head_sha, 'head');
  assert.equal(result.run_id, 3);
});
test('ignores successful runs on a different head', async () => {
  assert.equal((await probe({ runs: [{ ...successRun, head_sha: 'old' }] })).all_passed, false);
});
test('does not substitute another workflow event for PR CI', async () => {
  assert.equal((await probe({ runs: [{ ...successRun, event: 'workflow_dispatch' }] })).all_passed, false);
});
test('new pending run supersedes older success', async () => {
  const result = await probe({ runs: [successRun,
    { ...successRun, id: 4, status: 'in_progress', conclusion: null }] });
  assert.equal(result.run_id, 4);
  assert.equal(result.all_passed, false);
});
test('missing required job fails closed', async () => {
  assert.equal((await probe({ jobs: requiredJobs.slice(1) })).all_passed, false);
});
for (const conclusion of ['failure', 'cancelled', 'skipped']) {
  test(`${conclusion} job cannot count as passed`, async () => {
    assert.equal((await probe({ jobs: requiredJobs.map(job => ({ ...job, conclusion })) })).all_passed, false);
  });
}
test('API errors cannot produce successful evidence', async () => {
  await assert.rejects(probe({ failure: true }), /GitHub unavailable/);
});
