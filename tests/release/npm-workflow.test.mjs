import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

const workflow = readFileSync(new URL('../../.github/workflows/publish-npm.yml', import.meta.url), 'utf8');
// Exercise the actual workflow script against an isolated npm executable.
const script = workflow.split("node --input-type=module <<'NODE'\n")[1]
  .split('\n          NODE')[0].split('\n').map(line => line.slice(10)).join('\n');

function probe({ version = '0.3.2', versions = ['0.3.1'], error = false, mismatch = false }) {
  const root = mkdtempSync(join(tmpdir(), 'gizway-workflow-'));
  try {
    mkdirSync(join(root, 'sdk/web'), { recursive: true });
    mkdirSync(join(root, 'bin'));
    writeFileSync(join(root, 'sdk/web/package.json'), JSON.stringify({
      name: '@gizclaw/gizway', version, publishConfig: { registry: 'https://npm.pkg.github.com' },
    }));
    writeFileSync(join(root, 'sdk/web/package-lock.json'), JSON.stringify({
      version, packages: { '': { version: mismatch ? '0.0.0' : version } },
    }));
    writeFileSync(join(root, 'bin/npm'), '#!/bin/sh\nif [ "$REGISTRY_ERROR" = 1 ]; then exit 1; fi\nprintf "%s" "$REGISTRY_VERSIONS"\n', { mode: 0o755 });
    const output = join(root, 'output');
    const result = spawnSync(process.execPath, ['--input-type=module', '-'], {
      cwd: root, input: script, encoding: 'utf8',
      env: { ...process.env, PATH: `${join(root, 'bin')}:${process.env.PATH}`,
        GITHUB_OUTPUT: output, REGISTRY_ERROR: error ? '1' : '0',
        REGISTRY_VERSIONS: JSON.stringify(versions) },
    });
    return { status: result.status, stderr: result.stderr,
      output: existsSync(output) ? readFileSync(output, 'utf8') : '' };
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

for (const [name, options, expected] of [
  ['existing version skips', { version: '0.3.1' }, 'published=true\ntag=latest\n'],
  ['single-version registry response skips', { version: '0.3.1', versions: '0.3.1' }, 'published=true\ntag=latest\n'],
  ['new stable version publishes', {}, 'published=false\ntag=latest\n'],
  ['prerelease uses next', { version: '0.4.0-rc.1' }, 'published=false\ntag=next\n'],
]) {
  test(name, () => {
    const result = probe(options);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.output, expected);
  });
}

for (const [name, options] of [
  ['registry failure stops publication', { error: true }],
  ['lockfile mismatch stops publication', { mismatch: true }],
]) {
  test(name, () => {
    const result = probe(options);
    assert.notEqual(result.status, 0);
    assert.equal(result.output, '');
  });
}

test('publication preserves pending runs within the GitHub queue limit', () => {
  const concurrency = workflow.split('\nconcurrency:\n')[1].split('\njobs:')[0];
  assert.match(concurrency, /^  group: publish-gizway-npm$/m);
  assert.match(concurrency, /^  queue: max$/m);
  assert.match(concurrency, /^  cancel-in-progress: false$/m);
});
