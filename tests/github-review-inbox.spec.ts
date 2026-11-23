import type {
  RepositoryPolicy,
  ReviewJob,
  ReviewSnapshot,
  TestRecommendation,
} from '../apps/dashboard/src/features/github-review/api';
import { expect, menuItem, test } from './helpers';
import { answerReviewRead } from './github-review-fixtures';

test('opens one exact PR by link while discovery is paused', async ({
  page,
}) => {
  const repository = 'example/worker';
  const job: ReviewJob = {
    repository,
    number: 12,
    baseSha: 'b'.repeat(40),
    headSha: 'a'.repeat(40),
    source: 'repository',
    title: 'Verify worker initialization',
    status: 'queued',
    updatedAt: '2026-11-21T03:00:00Z',
  };
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 1,
      enabled: false,
      revision: 3,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: '2026-11-22T00:00:00Z' },
    jobs: [],
  };
  const writes: unknown[] = [];
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    expect(request.method()).toBe('POST');
    expect(new URL(request.url()).pathname).toBe(
      '/api/github-review/review-pr',
    );
    const body = request.postDataJSON();
    writes.push(body);
    if (body.repository !== repository) {
      await route.fulfill({
        status: 409,
        json: {
          code: 'github_review_out_of_scope',
          error:
            'Add this repository in Settings or request review from your connected account.',
        },
      });
      return;
    }
    snapshot.jobs = [job];
    const { number, baseSha, headSha, status } = job;
    await route.fulfill({
      json: {
        ...snapshot,
        jobs: [],
        hasJobs: true,
        selectedReview: { repository, number, baseSha, headSha, status },
      },
    });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();

  const panel = page.locator('[data-app-id="github-review"]');
  const start = panel.getByRole('button', { name: 'Review a PR', exact: true });
  const form = panel.getByRole('form', {
    name: 'Review a GitHub pull request',
  });
  const input = form.getByRole('textbox', { name: 'GitHub pull request URL' });
  const submit = form.getByRole('button', {
    name: 'Queue review',
    exact: true,
  });
  await expect(panel.getByRole('button', { name: 'Scan now' })).toBeDisabled();
  await start.click();
  await expect(input).toBeFocused();
  await input.press('Escape');
  await expect(form).toBeHidden();
  await expect(start).toBeFocused();
  await start.click();
  for (const invalid of [
    'https://github.com/example/worker/pull/12?diff=split',
    'https://name@github.com/example/worker/pull/12',
    'https://github.com.example.com/example/worker/pull/12',
    'https://github.com/example/../pull/12',
    `https://github.com/${'x'.repeat(40)}/worker/pull/12`,
  ]) {
    await input.fill(invalid);
    await input.press('Enter');
    await expect(form.getByRole('alert')).toContainText('Use a link like');
    await expect(input).toBeFocused();
  }
  expect(writes).toEqual([]);

  const outside = 'https://github.com/example/outside/pull/13';
  await input.fill(outside);
  await submit.click();
  await expect(form.getByRole('alert')).toContainText(
    'Add this repository in Settings',
  );
  await expect(input).toHaveValue(outside);
  await input.fill('https://github.com/example/worker/pull/12');
  await submit.click();
  const detail = panel.getByRole('article', {
    name: 'Review of example/worker #12',
  });
  await expect(form).toBeHidden();
  await expect(detail).toContainText('Waiting for daily reset');
  await expect(detail).toContainText('aaaaaaaaaaaa');
  await expect(panel.getByText('Review queued', { exact: true })).toBeVisible();
  expect(writes).toEqual([
    { repository: 'example/outside', number: 13, revision: 3 },
    { repository, number: 12, revision: 3 },
  ]);
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  await panel.getByRole('button', { name: 'In progress', exact: true }).click();
  const row = panel.getByRole('button', {
    name: 'Open example/worker #12 at aaaaaaaaaaaa',
  });
  await expect(row).toContainText('Waiting for daily reset');
  await expect(row.locator('[data-state="queued"]')).toBeVisible();
});

test('shows a missing repository VM before asking for a candidate assertion', async ({
  page,
}) => {
  const repository = 'example/worker';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const updatedAt = '2026-11-21T03:00:00Z';
  const job: ReviewJob = {
    repository,
    number: 12,
    headSha,
    baseSha,
    mergeBaseSha: 'c'.repeat(40),
    source: 'repository',
    status: 'analyzed',
    updatedAt,
    findings: [
      {
        title: 'Preserve the worker response',
        path: 'worker.go',
        line: 21,
        severity: 'medium',
        body: 'Callers rely on the previous behavior.',
      },
    ],
    candidates: [
      {
        id: 'candidate',
        trackId: 'track',
        index: 0,
        revision: 1,
        status: 'pending',
        note: '',
      },
    ],
  };
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    comparisonReady: true,
    repositoryEnvironments: {
      [repository]: { name: 'Worker review VM', configured: false },
    },
    budget: { used: 1, resetsAt: updatedAt },
    jobs: [job],
  };
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    expect(route.request().method()).toBe('GET');
    await route.fulfill({ json: snapshot });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();

  const panel = page.locator('[data-app-id="github-review"]');
  await panel
    .getByRole('button', { name: 'Review candidate for example/worker #12' })
    .click();
  const candidate = panel.getByRole('region', {
    name: 'Local candidate comments',
  });
  const evidence = candidate.locator(
    'details.github-review-candidate-evidence',
  );
  const compare = candidate.getByRole('button', {
    name: 'Compare revisions',
    exact: true,
  });
  const form = candidate.getByRole('form', { name: 'Compare revisions' });
  await expect(evidence).not.toHaveAttribute('open', '');
  await expect(compare).toBeVisible();
  await expect(compare).toBeDisabled();
  await expect(
    candidate.getByText(
      'Configure the dedicated review VM to run a comparison.',
    ),
  ).toBeVisible();
  await expect(form).toHaveCount(0);

  snapshot.repositoryEnvironments![repository].configured = true;
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(compare).toBeHidden();
  await evidence.getByText('Candidate evidence', { exact: true }).click();
  await compare.click();
  await expect(form.getByRole('textbox', { name: 'Assertion' })).toBeVisible();
});

test('automation shortcuts show the relevant settings and preserve unsaved review rules', async ({
  page,
}) => {
  const repository = 'example/worker';
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 1,
      enabled: false,
      revision: 3,
      repositoryPolicies: {
        [repository]: {
          rules: '',
          testCommand: [],
          autoCandidateValidation: true,
          comparisonScenarios: [
            {
              id: '1'.repeat(64),
              name: 'Worker initialization',
              title: 'Preserve worker initialization',
              path: 'worker.go',
              side: 'head',
              sourceComparisonId: '11111111-1111-4111-8111-111111111111',
            },
          ],
        },
      },
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: '2026-11-21T00:00:00Z' },
    jobs: [],
  };
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    expect(route.request().method()).toBe('GET');
    await route.fulfill({ json: snapshot });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();

  const panel = page.locator('[data-app-id="github-review"]');
  const inbox = panel.getByRole('tab', { name: 'Pull requests', exact: true });
  const settings = panel.getByRole('tab', { name: 'Settings', exact: true });
  const automation = panel.getByRole('region', { name: 'Automation status' });
  const schedule = panel.getByRole('group', {
    name: 'Scan settings',
    exact: true,
  });
  const repositories = panel.getByRole('group', {
    name: 'Repository settings',
    exact: true,
  });
  const connections = panel.getByRole('group', {
    name: 'Review connections',
    exact: true,
  });
  const configure = repositories.getByRole('button', {
    name: 'Configure example/worker',
    exact: true,
  });
  const rules = panel.getByRole('textbox', {
    name: 'Review rules',
    exact: true,
  });

  await panel.locator('.review-automation-summary').click();
  await expect(automation).toContainText('Paused in Settings');
  await expect(automation).toContainText('Review VM needs setup');
  await expect(automation).toContainText('Codex reviews today · 1 of 1.');
  await expect(automation).toContainText('New Codex reviews wait until');
  await automation.getByText('VM setup needed for 1').click();
  await expect(automation.getByText(repository, { exact: true })).toBeVisible();
  await automation
    .getByRole('button', { name: 'Open repository settings' })
    .click();
  await expect(settings).toHaveAttribute('aria-selected', 'true');
  await expect(repositories).toBeFocused();
  await configure.click();
  const ruleSection = panel
    .locator('summary')
    .filter({ hasText: /^Review rules/ });
  await ruleSection.click();
  await rules.fill('Keep initialization ordered.');

  await inbox.click();
  await automation
    .getByRole('button', { name: 'Open review schedule' })
    .click();
  await expect(schedule).toBeFocused();
  await expect(
    panel.getByRole('checkbox', { name: 'Scan GitHub automatically' }),
  ).not.toBeChecked();
  await configure.click();
  await ruleSection.click();
  await expect(rules).toHaveValue('Keep initialization ordered.');

  snapshot.config.enabled = true;
  snapshot.github.available = false;
  snapshot.github.connected = false;
  await inbox.click();
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(automation).toContainText('GitHub unavailable');
  await expect(
    automation.getByRole('button', { name: 'Open connections', exact: true }),
  ).toHaveCount(1);
  await automation.getByRole('button', { name: 'Open connections' }).click();
  await expect(connections).toBeFocused();
  await expect(
    panel.getByText('Unsaved changes', { exact: true }),
  ).toBeVisible();
});

test('confirms the displayed test once and presents its reusable receipt without a write', async ({
  page,
}) => {
  const repository = 'example/worker';
  const baseSha = 'b'.repeat(40);
  const headSha = 'a'.repeat(40);
  const updatedAt = '2026-11-20T03:00:00Z';
  const profile = {
    id: 'worker',
    name: 'Worker integration',
    entryPath: 'worker.go',
    command: ['go', 'test', './worker'],
  };
  const policy = { rules: '', testCommand: [], testProfiles: [profile] };
  const recommendation: TestRecommendation = {
    id: 'c'.repeat(64),
    status: 'run',
    reason:
      'Repository rules selected this test for these commits in attempt 1.',
    testName: profile.name,
    testId: profile.id,
    command: profile.command,
    baseSha,
    headSha,
    latestAttempt: 1,
  };
  const evidence: NonNullable<ReviewJob['evidence']> = {
    policy,
    review: { status: 'completed' },
    checks: { status: 'passed' },
    tests: [
      { attempt: 1, status: 'error', error: 'The checkout was not verified.' },
    ],
  };
  const job: ReviewJob = {
    repository,
    number: 12,
    baseSha,
    headSha,
    evidence,
    source: 'repository',
    title: 'Verify worker integration',
    status: 'clean',
    updatedAt,
    quickTestRecommendation: recommendation,
  };
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 3,
      repositoryPolicies: { [repository]: policy },
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: updatedAt },
    jobs: [job],
  };
  let writes = 0;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    if (request.method() === 'POST') {
      expect(new URL(request.url()).pathname).toBe(
        '/api/github-review/confirm-test-recommendation',
      );
      expect(request.postDataJSON()).toEqual({
        repository,
        number: 12,
        baseSha,
        headSha,
        revision: 3,
        recommendationId: recommendation.id,
      });
      writes++;
      evidence.tests.push({ attempt: 2, status: 'queued' });
      delete job.quickTestRecommendation;
    } else {
      expect(request.method()).toBe('GET');
    }
    return route.fulfill({ json: snapshot });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();
  const panel = page.locator('[data-app-id="github-review"]');
  await panel
    .getByRole('button', { name: 'Open example/worker #12 at aaaaaaaaaaaa' })
    .click();
  await expect(panel.locator('.github-review-pr-header')).toContainText(
    'No findings',
  );
  await expect(
    panel.getByRole('region', { name: 'Local candidate comments' }),
  ).toHaveCount(0);
  const disclosure = panel.locator('.github-review-evidence-summary');
  await expect(disclosure).toContainText('Quick test · Unavailable');
  await disclosure.click();
  const quick = panel.getByRole('region', {
    name: 'Local quick test',
    exact: true,
  });
  const recommended = quick.getByRole('region', {
    name: 'Recommended quick test',
  });
  const confirm = recommended.getByRole('button', {
    name: 'Run recommended test',
  });
  await expect(recommended).toContainText('Run on PR head aaaaaaaaaaaa');
  await expect(recommended).toContainText('Base bbbbbbbbbbbb');
  await expect(recommended).toContainText(recommendation.reason);
  expect(writes).toBe(0);
  await confirm.click();
  await expect(quick.getByText('Queued', { exact: true })).toBeVisible();
  await expect(confirm).toHaveCount(0);
  evidence.tests[1] = {
    attempt: 2,
    status: 'passed',
    checkoutSha: headSha,
    log: 'Verified worker behavior.',
    reusedFrom: { baseSha: 'c'.repeat(40), attempt: 4 },
  };
  job.quickTestRecommendation = {
    ...recommendation,
    status: 'reuse',
    latestAttempt: 2,
    reusableAttempt: 2,
    reusableStatus: 'passed',
  };
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(
    quick.getByRole('button', { name: 'Local quick test details' }),
  ).toContainText('Receipt available');
  await quick.getByRole('button', { name: 'Local quick test details' }).click();
  await expect(recommended).toHaveCount(0);
  await expect(quick.getByLabel('Test output for attempt 2')).toHaveText(
    'Verified worker behavior.',
  );
  await expect(quick).toContainText(
    'Reused test attempt 4 from base cccccccccccc.',
  );
  evidence.tests.push({ attempt: 3, status: 'error' });
  job.quickTestRecommendation = {
    ...job.quickTestRecommendation,
    latestAttempt: 3,
  };
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(quick.locator('.github-review-stage-summary')).toContainText(
    'Receipt available from attempt 2',
  );
  const prior = quick.getByText('Attempt 2 · Passed · Reusable receipt');
  await prior.click();
  await expect(quick.getByLabel('Test output for attempt 2')).toHaveText(
    'Verified worker behavior.',
  );
  await expect(confirm).toHaveCount(0);
  expect(writes).toBe(1);
});

test('prioritizes actionable pull requests and preserves completed and superseded reviews', async ({
  page,
}) => {
  const repository = 'example/worker';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const priorBaseSha = 'c'.repeat(40);
  const updatedAt = '2026-11-20T03:00:00Z';
  const policy: RepositoryPolicy = { rules: '', testCommand: [] };
  const evidence: NonNullable<ReviewJob['evidence']> = {
    review: { status: 'completed' },
    policy,
    checks: {
      status: 'passed',
      updatedAt,
      runs: [{ name: 'Lint', status: 'completed', conclusion: 'success' }],
    },
    tests: [],
  };
  const candidate = {
    id: 'candidate',
    trackId: 'track',
    note: '',
    index: 0,
    revision: 1,
  };
  const job = (number: number, fields: Partial<ReviewJob> = {}): ReviewJob => ({
    repository,
    number,
    headSha,
    baseSha,
    status: 'analyzed',
    source: 'repository',
    updatedAt,
    evidence,
    ...fields,
  });
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: updatedAt },
    jobs: [
      job(6, {
        status: 'clean',
      }),
      job(11, {
        evidence: { ...evidence, checks: { status: 'empty', updatedAt } },
      }),
      job(8, {
        status: 'stale',
      }),
      job(5, {
        status: 'reviewing',
        evidence: { ...evidence, review: { status: 'running' } },
      }),
      job(1, {
        candidates: [{ ...candidate, status: 'pending' }],
      }),
      job(6, {
        status: 'stale',
        baseSha: priorBaseSha,
        observedBaseSha: baseSha,
      }),
      job(2, {
        evidence: {
          ...evidence,
          checks: {
            ...evidence.checks,
            error: 'GitHub timed out.',
            refreshErrorAt: updatedAt,
          },
        },
      }),
      job(9, {
        status: 'stale',
        observedBaseSha: 'e'.repeat(40),
      }),
      job(4, {
        findings: [
          'Preserve the worker response',
          'Report an incomplete read',
        ].map((title, index) => ({
          title,
          path: 'worker.go',
          line: 21 + index,
          severity: 'medium',
          body: 'Callers rely on the previous behavior.',
        })),
        evidence: {
          ...evidence,
          checks: { status: 'failed', error: 'The check failed.' },
        },
        candidates: [
          {
            ...candidate,
            status: 'confirmed',
            commentDraft: {
              body: 'Expected the original result.',
              revision: 1,
              baseSha,
              headSha,
              anchorSha: headSha,
              path: 'worker.go',
              line: 21,
              side: 'head',
              stale: false,
              updatedAt,
            },
          },
          {
            ...candidate,
            id: 'another-candidate',
            trackId: 'another-track',
            status: 'pending',
            index: 1,
          },
        ],
      }),
      job(3, {
        evidence: {
          ...evidence,
          policy: { ...policy, testCommand: ['go', 'test', './worker'] },
        },
      }),
      job(10, {
        status: 'failed',
        evidence: {
          ...evidence,
          review: { status: 'failed' },
        },
      }),
    ],
  };
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    expect(route.request().method()).toBe('GET');
    await route.fulfill({ json: snapshot });
  });
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();
  const panel = page.locator('[data-app-id="github-review"]');
  const filter = (name: string) =>
    panel.getByRole('button', { name, exact: true });
  await expect(filter('Needs action')).toHaveAttribute('aria-pressed', 'true');
  const row = (group: string, number: number, head = headSha) =>
    panel
      .getByRole('region', { name: group, exact: true })
      .getByRole('button', {
        name: `Open ${repository} #${number} at ${head.slice(0, 12)}`,
        exact: true,
      });
  const actions = panel.getByRole('region', {
    name: 'Needs action',
    exact: true,
  });
  await expect(row('Needs action', 1)).toContainText('1 to assess');
  await expect(row('Needs action', 2)).toContainText('Upstream CI');
  await expect(row('Needs action', 3)).toContainText('Quick test');
  await expect(row('Needs action', 9)).toContainText('Target branch changed');
  await expect(row('Needs action', 10)).toContainText('Codex review');
  const mixed = row('Needs action', 4);
  await expect(mixed).toContainText('1 draft to review');
  await expect(mixed).toContainText('1 to assess');
  await expect(mixed).toContainText('1 blocker');
  await expect(row('In progress', 5)).toBeHidden();
  await expect(row('Completed', 6)).toBeHidden();

  const back = panel.getByRole('button', { name: '← Pull requests' });
  const action = (name: string) =>
    actions.getByRole('button', {
      name: `${name} for ${repository} #4`,
      exact: true,
    });
  await mixed.click();
  await expect(
    panel.getByRole('heading', { name: 'Preserve the worker response' }),
  ).toBeInViewport();
  await expect(panel.getByRole('region', { name: 'Upstream CI' })).toBeHidden();
  await back.click();

  await action('Review draft').click();
  await expect(
    panel.getByRole('heading', { name: 'Preserve the worker response' }),
  ).toBeVisible();
  await expect(panel.getByText('Expected the original result.')).toBeVisible();
  await expect(panel.getByText('The check failed.')).toBeHidden();
  await back.click();
  await expect(action('Review draft')).toBeFocused();

  await action('Review candidate').click();
  await expect(
    panel.getByRole('heading', { name: 'Report an incomplete read' }),
  ).toBeVisible();
  await expect(
    panel.getByRole('button', { name: 'Assess candidate' }),
  ).toBeVisible();
  await back.click();
  await action('View blocker').click();
  await expect(panel.getByText('The check failed.')).toBeVisible();
  await back.click();

  await filter('In progress').click();
  await expect(row('In progress', 5)).toBeVisible();
  await filter('History').click();
  await expect(row('Completed', 6)).toContainText('Checks: Passed');
  await expect(row('Completed', 6)).toContainText(
    `Base ${baseSha.slice(0, 12)}`,
  );
  await expect(row('Completed', 11)).toContainText('Checks: No checks');
  await expect(row('Superseded', 8)).toBeVisible();
  const earlier = row('Superseded', 6);
  await expect(earlier).toContainText(`Base ${priorBaseSha.slice(0, 12)}`);
  await earlier.click();
  await expect(back).toBeFocused();
  await panel.getByText('View evidence', { exact: true }).click();
  await expect(
    panel.getByRole('button', { name: 'Review latest commit' }),
  ).toBeVisible();
  await panel.getByText('Commits and review rules', { exact: true }).click();
  await expect(panel.getByText(priorBaseSha, { exact: true })).toBeVisible();
  await back.click();
  await expect(earlier).toBeFocused();
});

test('can open and return to an older actionable review beyond the first inbox page', async ({
  page,
}) => {
  const account = 'fixture-reviewer';
  const repository = 'example/older-worker';
  const newerRepo = 'example/newer-worker';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const updatedAt = '2026-11-10T03:00:00Z';
  const cursor = 'older-action-page';
  const finding = {
    title: 'Preserve the worker response',
    body: 'Clients still expect a response when the input is empty.',
    severity: 'medium',
    path: 'worker.go',
    line: 14,
  };
  const older: ReviewJob = {
    repository,
    number: 12,
    headSha,
    baseSha,
    source: 'repository',
    status: 'analyzed',
    title: 'Retain old review through busy intake',
    updatedAt,
    findings: [finding],
    candidates: [
      {
        id: 'candidate',
        index: 0,
        trackId: 'track',
        note: '',
        revision: 1,
        status: 'pending',
      },
    ],
  };
  const first = {
    ...older,
    repository: newerRepo,
    number: 99,
    updatedAt: '2026-11-20T03:00:00Z',
  };
  const snapshot: ReviewSnapshot = {
    github: { account, connected: true, available: true },
    codex: { connected: true, available: true },
    config: {
      intervalMinutes: 10,
      dailyLimit: 10,
      revision: 1,
      enabled: false,
      repositories: [newerRepo, repository],
      publishRepositories: [],
    },
    budget: { used: 2, resetsAt: updatedAt },
    jobs: [first],
  };
  const detailRequests: Record<string, string>[] = [];
  await page.route('**/api/github-review**', async (route) => {
    const request = route.request();
    expect(request.method()).toBe('GET');
    const endpoint = new URL(request.url());
    if (endpoint.pathname === '/api/github-review/inbox') {
      expect(endpoint.searchParams.get('view')).toBe('action');
      const pageCursor = endpoint.searchParams.get('cursor');
      const entry = pageCursor ? older : first;
      if (pageCursor) expect(pageCursor).toBe(cursor);
      return route.fulfill({
        json: {
          account,
          jobs: [{ ...entry, findings: undefined, inboxGroup: 'action' }],
          ...(pageCursor ? {} : { nextCursor: cursor }),
        },
      });
    }
    if (endpoint.pathname === '/api/github-review/job') {
      detailRequests.push(Object.fromEntries(endpoint.searchParams));
      if (await answerReviewRead(route, { ...snapshot, jobs: [older] })) return;
    }
    if (await answerReviewRead(route, snapshot)) return;
    await route.fulfill({ json: snapshot });
  });

  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await page
    .getByRole('menu', { name: 'Applications', exact: true })
    .getByRole('menuitem', { name: 'GitHub Review', exact: true })
    .click();
  const panel = page.locator('[data-app-id="github-review"]');
  const next = panel.getByRole('button', { name: 'Next page', exact: true });
  const previous = panel.getByRole('button', {
    name: 'Previous page',
    exact: true,
  });
  const newerRow = panel.getByRole('button', {
    name: `Open ${newerRepo} #99 at ${headSha.slice(0, 12)}`,
  });
  const olderRow = panel.getByRole('button', {
    name: `Open ${repository} #12 at ${headSha.slice(0, 12)}`,
  });
  await expect(newerRow).toBeVisible();
  await expect(olderRow).toBeHidden();
  await next.click();
  await expect(olderRow).toBeVisible();
  await olderRow.click();
  await expect(
    panel.getByRole('heading', { name: finding.title }),
  ).toBeVisible();
  expect(detailRequests).toContainEqual({
    repository,
    number: '12',
    headSha,
    baseSha,
  });
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  await expect(olderRow).toBeFocused();
  await previous.click();
  await expect(newerRow).toBeVisible();
  await expect(olderRow).toBeHidden();
});
