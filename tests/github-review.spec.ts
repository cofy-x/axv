import type {
  CandidateDecision,
  CandidateCommentDraft,
  CandidateOccurrence,
  EvidenceItem,
  ReviewConfig,
  ReviewRetry,
  ReviewSnapshot,
} from '../apps/dashboard/src/features/github-review/api';
import type { Locator } from '@playwright/test';
import { expect, menuItem, test } from './helpers';
import { answerReviewRead } from './github-review-fixtures';

async function showEvidence(panel: Locator) {
  const evidence = panel.locator('details.github-review-overview');
  if ((await evidence.getAttribute('open')) === null)
    await evidence.getByText('View evidence', { exact: true }).click();
}

function configureRepository(panel: Locator, repository: string) {
  return panel.getByRole('button', {
    name: `Configure ${repository}`,
    exact: true,
  });
}

function backToRepositories(panel: Locator) {
  return panel.getByRole('button', {
    name: 'Back to repositories',
    exact: true,
  });
}

async function openRepositorySettings(panel: Locator, repository: string) {
  const back = backToRepositories(panel);
  if (await back.isVisible()) await back.click();
  await configureRepository(panel, repository).click();
}

async function openPolicySection(panel: Locator, label: string) {
  const summary = panel.locator('summary').filter({
    hasText: new RegExp(`^${label}`),
  });
  if ((await summary.locator('..').getAttribute('open')) === null)
    await summary.click();
}

async function showStageDetails(panel: Locator, title: string) {
  await showEvidence(panel);
  const stage = panel.getByRole('region', { name: title, exact: true });
  await expect(stage).toBeVisible();
  const collapsed = stage
    .getByRole('button', { name: `${title} details`, exact: true })
    .and(stage.locator('[aria-expanded="false"]'));
  if (await collapsed.count()) await collapsed.click();
  return stage;
}

test('enables automatic candidate verification only for a repository with an approved scenario', async ({
  page,
}) => {
  const firstRepository = 'example/service';
  const approvedRepository = 'example/toolkit';
  const scenario = {
    id: 'approved-scenario',
    name: 'Preserve a stopped operation result',
    path: 'worker.go',
    title: 'Swallows the original operation error',
    side: 'head' as const,
    sourceComparisonId: 'source-comparison',
  };
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [firstRepository, approvedRepository],
      publishRepositories: [],
      repositoryPolicies: {
        [approvedRepository]: {
          rules: '',
          testCommand: [],
          comparisonScenarios: [scenario],
        },
      },
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    comparisonReady: true,
    budget: { used: 0, resetsAt: '2026-11-22T00:00:00Z' },
    jobs: [],
  };
  let saves = 0;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    if (request.method() === 'PUT') {
      expect(new URL(request.url()).pathname).toBe('/api/github-review/config');
      const config = request.postDataJSON() as ReviewConfig;
      expect(config.revision).toBe(snapshot.config.revision);
      const editedPolicy = config.repositoryPolicies?.[approvedRepository];
      expect(editedPolicy).toEqual({
        rules: '',
        testCommand: [],
        ...(saves === 0 ? { autoCandidateValidation: true } : {}),
      });
      snapshot.config = {
        ...config,
        revision: config.revision + 1,
        repositoryPolicies: {
          ...config.repositoryPolicies,
          [approvedRepository]: {
            ...editedPolicy!,
            comparisonScenarios: [scenario],
          },
        },
      };
      saves++;
    } else {
      expect(request.method()).toBe('GET');
    }
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
  await panel.getByRole('tab', { name: 'Settings', exact: true }).click();
  await openRepositorySettings(panel, firstRepository);
  const enabled = panel.getByRole('checkbox', {
    name: 'Enable automatic candidate verification',
    exact: true,
  });
  const approved = panel.getByRole('list', {
    name: 'Approved comparison scenarios',
  });
  const save = panel.getByRole('button', {
    name: 'Save settings',
    exact: true,
  });

  await openPolicySection(panel, 'Automatic candidate verification');
  await expect(enabled).toBeDisabled();
  await expect(approved).toHaveCount(0);
  await openRepositorySettings(panel, approvedRepository);
  await openPolicySection(panel, 'Automatic candidate verification');
  await expect(approved).toContainText(scenario.name);
  await expect(approved).toContainText(scenario.path);
  await expect(enabled).not.toBeChecked();
  await enabled.check();
  await save.click();
  await expect(panel.getByRole('status')).toHaveText('Settings saved');
  await expect(enabled).toBeChecked();
  await expect(approved).toContainText(scenario.title);
  await panel.getByRole('tab', { name: 'Pull requests', exact: true }).click();
  const automation = panel.getByRole('region', {
    name: 'Automation status',
    exact: true,
  });
  const automationSummary = panel.locator('.review-automation-summary');
  await expect(automationSummary).toContainText('reviews today');
  await expect(automation).toHaveCount(0);
  await automationSummary.click();
  await expect(automation).toContainText('PR discovery');
  await expect(automation).toContainText('Paused in Settings');
  await expect(automation).toContainText('Candidate verification');
  await expect(automation).toContainText('Enabled for 1 repository');
  snapshot.config.enabled = true;
  snapshot.discovery = {
    scanning: false,
    nextScanAt: new Date(Date.now() + 600_000).toISOString(),
    lastSuccessAt: new Date().toISOString(),
  };
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(automation).toContainText('Next scan');
  await expect(automation).toContainText('Last successful scan');
  snapshot.discovery.error = 'GitHub did not answer.';
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(automationSummary).toContainText('Scan failed');
  await expect(automation.getByText(snapshot.discovery.error)).toBeVisible();
  await expect(automation).toContainText('Last successful scan');
  delete snapshot.discovery.error;
  snapshot.config.enabled = false;
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(automation).toContainText('Paused in Settings');
  await panel.getByRole('tab', { name: 'Settings', exact: true }).click();
  await expect(backToRepositories(panel)).toBeVisible();
  await expect(approved).toContainText(scenario.name);

  await enabled.uncheck();
  await save.click();
  await expect(panel.getByRole('status')).toHaveText('Settings saved');
  await expect(enabled).not.toBeChecked();
  await expect(approved).toContainText(scenario.name);
  expect(saves).toBe(2);
});

test('switches candidates and saves a local draft at its reviewed line', async ({
  page,
}) => {
  const repository = 'example/worker';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const updatedAt = '2026-11-19T03:00:00Z';
  const first = 'Do not swallow canceled operations';
  const second = 'Return the original read error';
  const markdown =
    '**Reproduction**\n\n```text\nexpected: error\nobserved: success\n```';
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 5,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: updatedAt },
    jobs: [
      {
        repository,
        number: 5,
        headSha,
        baseSha,
        status: 'analyzed',
        title: 'Release the canceled worker',
        source: 'repository',
        updatedAt,
        findings: [first, second].map((title, index) => ({
          title,
          path: 'worker.go',
          line: index + 20,
          severity: 'medium',
          body: 'A caller relies on the original result.',
        })),
        candidates: ['first', 'second'].map((id, index) => ({
          id,
          trackId: id,
          status: 'confirmed',
          note: 'Reproduced in the VM.',
          index,
          revision: 2,
        })),
      },
    ],
  };
  const job = snapshot.jobs[0];
  const candidate = job.candidates![1];
  let writes = 0;
  const reviewedWrites: boolean[] = [];
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === '/api/github-review/candidate-draft-reviewed') {
      expect(request.method()).toBe('POST');
      const { reviewed, ...anchor } = request.postDataJSON();
      expect(anchor).toEqual({
        repository,
        baseSha,
        headSha,
        account: 'fixture-reviewer',
        number: 5,
        candidateId: 'second',
        revision: candidate.commentDraft!.revision,
      });
      reviewedWrites.push(reviewed);
      candidate.commentDraft!.reviewedAt = reviewed ? updatedAt : undefined;
      return route.fulfill({ json: snapshot });
    }
    if (request.method() === 'POST') {
      expect(path).toBe('/api/github-review/candidate-draft');
      expect(request.postDataJSON()).toEqual({
        repository,
        baseSha,
        headSha,
        account: 'fixture-reviewer',
        number: 5,
        candidateId: 'second',
        revision: 0,
        body: markdown,
      });
      writes++;
      candidate.commentDraft = {
        body: markdown,
        revision: 1,
        baseSha,
        headSha,
        anchorSha: headSha,
        path: 'worker.go',
        line: 21,
        side: 'head',
        stale: false,
        updatedAt,
      } satisfies CandidateCommentDraft;
    } else {
      expect(request.method()).toBe('GET');
    }
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
  await panel.getByRole('button', { name: 'History', exact: true }).click();
  await panel
    .getByRole('button', { name: 'Open example/worker #5 at aaaaaaaaaaaa' })
    .click();
  const choices = panel.getByRole('list', { name: 'Choose a candidate' });
  const firstChoice = choices.getByRole('button', { name: new RegExp(first) });
  const secondChoice = choices.getByRole('button', {
    name: new RegExp(second),
  });
  const draft = panel.locator('details.github-review-draft');
  await expect(panel.getByRole('heading', { name: first })).toBeVisible();
  await secondChoice.click();
  await expect(panel.getByRole('heading', { name: second })).toBeVisible();
  await expect(panel.getByRole('heading', { name: first })).toHaveCount(0);
  await draft.locator(':scope > summary').click();
  await draft.getByRole('button', { name: 'Create draft' }).click();
  const input = draft.getByRole('textbox', { name: 'Comment Markdown' });
  await expect(input).toBeFocused();
  await input.fill(markdown);
  await firstChoice.click();
  await secondChoice.click();
  await expect(input).toHaveValue(markdown);
  await draft.getByRole('button', { name: 'Save local draft' }).click();
  const summary = draft.getByText('Local comment draft', { exact: true });
  await expect(draft.locator(':scope > summary')).toContainText(
    'Ready to review',
  );
  await summary.click();
  await expect(draft.locator('.github-review-draft-anchor')).toContainText(
    'worker.go:21',
  );
  await expect(draft.locator('.markdown-preview strong')).toHaveText(
    'Reproduction',
  );
  await expect(draft.locator('.markdown-preview pre')).toContainText(
    'observed: success',
  );
  await expect(
    draft.getByRole('button', { name: 'Markdown source' }),
  ).toBeHidden();
  await draft.getByText('Markdown tools', { exact: true }).click();
  await draft.getByRole('button', { name: 'Markdown source' }).click();
  await expect(draft.getByLabel('Markdown source', { exact: true })).toHaveText(
    markdown,
  );
  await draft.getByRole('button', { name: 'Edit draft', exact: true }).click();
  await input.fill('My unsaved revision');
  candidate.commentDraft = {
    ...candidate.commentDraft!,
    body: 'A separate window changed the draft.',
    revision: 2,
  };
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(draft.getByRole('alert')).toContainText(
    'The saved draft changed',
  );
  await expect(input).toHaveValue('My unsaved revision');
  await expect(
    draft.getByRole('button', { name: 'Save local draft' }),
  ).toBeDisabled();
  await draft.getByRole('button', { name: 'Keep my edits' }).click();
  await expect(
    draft.getByRole('button', { name: 'Save local draft' }),
  ).toBeEnabled();
  await draft.getByRole('button', { name: 'Cancel draft edit' }).click();
  await expect(draft.locator(':scope > summary')).toBeFocused();
  await summary.click();
  await draft
    .getByRole('button', { name: 'Mark reviewed', exact: true })
    .click();
  await expect(
    draft.getByRole('button', { name: 'Mark as unread', exact: true }),
  ).toBeVisible();
  await panel
    .getByRole('button', { name: '← Pull requests', exact: true })
    .click();
  const row = panel.getByRole('button', {
    name: 'Open example/worker #5 at aaaaaaaaaaaa',
  });
  await panel
    .getByRole('button', { name: 'Needs action', exact: true })
    .click();
  await expect(row).toHaveCount(0);
  await panel.getByRole('button', { name: 'History', exact: true }).click();
  await row.click();
  await secondChoice.click();
  await summary.click();
  await draft
    .getByRole('button', { name: 'Mark as unread', exact: true })
    .click();
  await expect(
    draft.getByRole('button', { name: 'Mark reviewed', exact: true }),
  ).toBeVisible();
  expect(reviewedWrites).toEqual([true, false]);
  job.status = 'stale';
  candidate.commentDraft!.stale = true;
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(draft.locator(':scope > summary')).toContainText(
    'Earlier revision',
  );
  await expect(draft.getByRole('button', { name: 'Edit draft' })).toHaveCount(
    0,
  );
  expect(writes).toBe(1);
});

test('configures repository policies and inspects evidence across VM retries and a new PR head', async ({
  page,
}, testInfo) => {
  const repositories = ['example/worker', 'example/agent-api'];
  const workerRepo = 'Example/Worker';
  const baseSha = 'c'.repeat(40);
  const headSha = 'a'.repeat(40);
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [],
      publishRepositories: [],
      intervalMinutes: 5,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: false },
    codex: { available: true, connected: false },
    jobs: [],
    budget: { used: 0, resetsAt: '2026-11-20T00:00:00+08:00' },
  };
  const saved: ReviewConfig[] = [];
  const reviewRetries: ReviewRetry[] = [];
  const testRetries: (ReviewRetry & { attempt: number })[] = [];
  const workerPolicy = {
    rules: 'Review cleanup after cancellation.',
    testCommand: ['go', 'test', './internal/queue'],
  };
  const apiPolicy = {
    rules: 'Check SDK compatibility.',
    testCommand: ['go', 'test', './pkg/api'],
  };
  let scans = 0;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === '/api/github-review/connect/github') {
      expect(request.method()).toBe('POST');
      snapshot.github.attempt = {
        status: 'pending',
        url: 'https://github.com/login/device',
        code: 'AAAA-BBBB',
      };
    } else if (path === '/api/github-review/connect/codex') {
      expect(request.method()).toBe('POST');
      snapshot.codex = {
        available: true,
        connected: true,
        account: 'ChatGPT',
      };
    } else if (path === '/api/github-review/config') {
      expect(request.method()).toBe('PUT');
      const data = request.postDataJSON() as ReviewConfig;
      saved.push(data);
      snapshot.config = { ...data, revision: data.revision + 1 };
    } else if (path === '/api/github-review/run') {
      expect(request.method()).toBe('POST');
      scans++;
      const updatedAt = '2026-11-19T03:00:00Z';
      snapshot.lastPollAt = updatedAt;
      snapshot.budget.used = 3;
      snapshot.jobs = [
        {
          repository: 'example/external',
          number: 19,
          source: 'requested',
          baseSha,
          headSha: 'b'.repeat(40),
          status: 'analyzed',
          title: 'Outside review request',
          findings: [
            {
              path: 'runner.go',
              line: 10,
              severity: 'P2',
              title: 'Return the read error',
              body: 'Subsequent work expects valid state.',
            },
          ],
          updatedAt,
        },
        {
          repository: repositories[0],
          number: 5,
          source: 'repository',
          baseSha,
          headSha,
          status: 'analyzed',
          title: 'Release the canceled worker',
          evidence: {
            policy: workerPolicy,
            review: {
              status: 'completed',
              messages: [
                {
                  at: updatedAt,
                  text: 'Checked the candidate locations against the captured diff.',
                },
              ],
              startedAt: '2026-11-19T02:59:00Z',
              updatedAt,
            },
            checks: {
              status: 'passed',
              runs: [
                {
                  name: 'Go unit tests',
                  status: 'completed',
                  conclusion: 'success',
                  url: 'https://github.com/example/worker/actions/runs/5',
                },
                ...Array.from({ length: 5 }, (_, index) => ({
                  name: `Package ${index + 1}`,
                  status: 'completed',
                  conclusion: 'success',
                })),
                {
                  name: 'Privileged E2E',
                  status: 'completed',
                  conclusion: 'skipped',
                },
              ],
              updatedAt,
            },
            tests: [
              {
                attempt: 1,
                status: 'failed',
                taskId: 'vm-task-5',
                attemptId: 'attempt-one',
                vmId: 'review-vm',
                projectId: 'dedicated-project',
                log: 'FAIL TestWorkerRelease',
                updatedAt,
              },
            ],
          },
          updatedAt,
        },
        {
          repository: repositories[1],
          number: 5,
          source: 'repository',
          baseSha,
          headSha,
          status: 'failed',
          error: 'Review timed out',
          updatedAt,
        },
      ];
    } else if (path === '/api/github-review/retry-test') {
      expect(request.method()).toBe('POST');
      const data = request.postDataJSON() as ReviewRetry & { attempt: number };
      testRetries.push(data);
      const command =
        snapshot.config.repositoryPolicies![workerRepo].testCommand;
      snapshot.jobs[1].evidence!.tests.push({
        attempt: data.attempt + 1,
        status: 'queued',
        command: [...command],
      });
    } else if (path === '/api/github-review/retry') {
      expect(request.method()).toBe('POST');
      const data = request.postDataJSON() as ReviewRetry;
      reviewRetries.push(data);
      snapshot.jobs = snapshot.jobs.map((job) =>
        job.repository === data.repository && job.number === data.number
          ? { ...job, status: 'queued', error: undefined }
          : job,
      );
    } else {
      expect(path).toBe('/api/github-review');
      expect(request.method()).toBe('GET');
    }
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
  const run = panel.getByRole('button', { name: 'Scan now' });
  const inbox = panel.getByRole('tab', { name: 'Pull requests' });
  const settings = panel.getByRole('tab', { name: 'Settings' });
  await expect(
    panel
      .getByRole('tablist', { name: 'GitHub Review sections' })
      .getByRole('tab'),
  ).toHaveText(['Pull requests', 'Settings']);
  await inbox.click();
  await expect(run).toBeDisabled();
  await settings.click();
  await panel.getByRole('button', { name: 'Connect GitHub' }).click();
  await expect(panel.getByText('AAAA-BBBB')).toBeVisible();
  await expect(
    panel.getByRole('link', { name: 'GitHub authorization' }),
  ).toHaveAttribute('href', 'https://github.com/login/device');
  snapshot.github = {
    available: true,
    connected: true,
    account: 'fixture-reviewer',
  };
  await expect(panel.getByText('fixture-reviewer')).toBeVisible();
  await panel.getByRole('button', { name: 'Connect Codex' }).click();
  await expect(panel.getByText('ChatGPT', { exact: true })).toBeVisible();

  const manage = panel.getByText('Manage connections', { exact: true });
  await expect(
    panel.getByRole('button', { name: 'Disconnect GitHub' }),
  ).toBeHidden();
  await manage.click();
  await expect(
    panel.getByRole('button', { name: 'Disconnect GitHub' }),
  ).toBeVisible();
  await manage.click();

  const editedRepositories = [workerRepo, repositories[1]];
  await panel
    .getByRole('button', { name: 'Add repository', exact: true })
    .click();
  await panel
    .getByRole('button', { name: 'Paste multiple repositories' })
    .click();
  await panel
    .getByLabel('Repository names (one owner/repo per line)')
    .fill([workerRepo, ...repositories].join('\n'));
  await panel.getByRole('button', { name: 'Add to list', exact: true }).click();
  await expect(configureRepository(panel, workerRepo)).toBeVisible();
  await expect(configureRepository(panel, repositories[1])).toBeVisible();
  const rules = panel.getByLabel('Review rules');
  const command = panel.getByLabel('VM test command');

  await openRepositorySettings(panel, workerRepo);
  await openPolicySection(panel, 'Review rules');
  await rules.fill(workerPolicy.rules);
  await openPolicySection(panel, 'VM tests');
  await command.fill(workerPolicy.testCommand.join('\n'));

  await openRepositorySettings(panel, repositories[1]);
  await openPolicySection(panel, 'Review rules');
  await expect(rules).toHaveValue('');
  await rules.fill(apiPolicy.rules);
  await openPolicySection(panel, 'VM tests');
  await command.fill(apiPolicy.testCommand.join('\n'));

  await openRepositorySettings(panel, workerRepo);
  await openPolicySection(panel, 'Review rules');
  await expect(rules).toHaveValue(workerPolicy.rules);
  await openPolicySection(panel, 'VM tests');
  await expect(command).toHaveValue(workerPolicy.testCommand.join('\n'));
  await backToRepositories(panel).click();
  await panel.getByLabel('Poll interval in minutes').fill('8');
  const limit = panel.getByLabel('Daily Codex review limit');
  await limit.fill('0');
  await panel.getByRole('button', { name: 'Save settings' }).click();
  await expect(limit).toBeFocused();
  await expect(limit).toHaveAttribute('aria-invalid', 'true');
  expect(saved).toHaveLength(0);
  await limit.fill('3');
  await panel
    .getByRole('checkbox', { name: 'Scan GitHub automatically' })
    .check();
  const includeRequests = panel.getByRole('checkbox', {
    name: 'Include direct review requests',
  });
  await expect(includeRequests).toBeChecked();
  await includeRequests.uncheck();
  await inbox.click();
  await expect(
    panel.getByRole('heading', { name: 'No reviews yet' }),
  ).toBeVisible();
  await panel.getByRole('tab', { name: 'Settings' }).click();
  for (const repository of editedRepositories)
    await expect(configureRepository(panel, repository)).toBeVisible();
  await panel.getByRole('button', { name: 'Save settings' }).click();
  await expect(panel.getByRole('status')).toHaveText('Settings saved');
  expect(saved).toEqual([
    {
      repositories: editedRepositories,
      publishRepositories: [],
      repositoryPolicies: {
        [workerRepo]: workerPolicy,
        [repositories[1]]: apiPolicy,
      },
      intervalMinutes: 8,
      watchOnly: true,
      dailyLimit: 3,
      enabled: true,
      revision: 1,
    },
  ]);
  await inbox.click();
  await run.click();
  await settings.click();
  await expect(
    panel.getByText('3 of 3 review starts used today.'),
  ).toBeVisible();
  await inbox.click();
  const automationSummary = panel.locator('.review-automation-summary');
  await expect(automationSummary).toContainText('Daily limit reached');
  await automationSummary.click();
  await expect(
    panel.getByRole('region', { name: 'Automation status' }),
  ).toContainText('Daily review limit reached.');
  const goBack = panel.getByRole('button', { name: '← Pull requests' });
  await panel
    .getByRole('button', { name: 'Open example/external #19 at bbbbbbbbbbbb' })
    .click();
  await expect(
    panel
      .getByRole('region', { name: 'Local candidate comments' })
      .getByText('Return the read error'),
  ).toBeVisible();
  await expect(panel.getByText('Requested from you')).toBeVisible();
  await showStageDetails(panel, 'Codex review');
  await expect(
    panel.getByRole('button', { name: 'Run with current rules' }),
  ).toBeEnabled();
  await goBack.click();
  await panel
    .getByRole('button', {
      name: 'Open example/agent-api #5 at aaaaaaaaaaaa',
    })
    .click();
  await showEvidence(panel);
  await panel.getByRole('button', { name: 'Retry review' }).click();
  await expect(
    panel.getByRole('status').getByText('Retry queued'),
  ).toBeVisible();
  await expect(
    panel.getByRole('region', { name: 'Codex review' }).getByText('Queued'),
  ).toBeVisible();
  await goBack.click();
  const sandbox = panel.getByRole('button', {
    name: 'Open example/worker #5 at aaaaaaaaaaaa',
  });
  const inboxShot = testInfo.outputPath('pull-requests.png');
  await panel.screenshot({ path: inboxShot });
  await testInfo.attach('Pull request inbox', {
    path: inboxShot,
    contentType: 'image/png',
  });
  await sandbox.click();
  const checks = panel.getByRole('region', { name: 'Upstream CI' });
  const vmTest = panel.getByRole('region', { name: 'Local quick test' });
  await expect(checks).toBeHidden();
  await expect(panel.getByText('FAIL TestWorkerRelease')).toBeHidden();
  await expect(checks.getByText('6 passed · 1 skipped')).toBeHidden();
  await showStageDetails(panel, 'Upstream CI');
  await expect(checks.getByText('6 passed · 1 skipped')).toBeVisible();
  const showChecks = checks.getByText('Show 7 check runs');
  await expect(checks.getByRole('link', { name: 'Go unit tests' })).toHaveCount(
    0,
  );
  await showChecks.click();
  await expect(
    checks.getByRole('link', { name: 'Go unit tests' }),
  ).toHaveAttribute('href', 'https://github.com/example/worker/actions/runs/5');
  await showChecks.click();
  await expect(vmTest.getByText('Failed', { exact: true })).toBeVisible();
  await expect(vmTest.getByText('FAIL TestWorkerRelease')).toBeVisible();
  const detailsShot = testInfo.outputPath('review-evidence.png');
  await panel.screenshot({ path: detailsShot });
  await testInfo.attach('PR evidence', {
    path: detailsShot,
    contentType: 'image/png',
  });
  await showStageDetails(panel, 'Codex review');
  await panel.getByText('Activity', { exact: true }).click();
  await expect(
    panel.getByText(
      'Checked the candidate locations against the captured diff.',
    ),
  ).toBeVisible();
  await vmTest.getByText('Execution receipt').click();
  await expect(vmTest.getByText('vm-task-5')).toBeVisible();
  await panel.getByText('Commits and review rules').click();
  await expect(
    panel.getByRole('article').getByText(workerPolicy.rules),
  ).toBeVisible();
  // A manual test is independent of automatic scans and the daily Codex budget.
  snapshot.config.enabled = false;
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  const retryTest = panel.getByRole('button', { name: 'Retry test' });
  await retryTest.click();
  await expect(
    panel.getByRole('status').getByText('Test retry queued'),
  ).toBeVisible();
  await expect(vmTest.getByText('Queued', { exact: true })).toBeVisible();
  await expect(checks.getByText('Passed', { exact: true })).toBeVisible();
  await showStageDetails(panel, 'Local quick test');
  await vmTest.getByText('Attempt 1 · Failed').click();
  await expect(vmTest.getByText('FAIL TestWorkerRelease')).toBeVisible();
  expect(reviewRetries).toEqual([
    { repository: repositories[1], number: 5, headSha, baseSha, revision: 2 },
  ]);
  expect(testRetries).toEqual([
    {
      repository: repositories[0],
      number: 5,
      headSha,
      baseSha,
      revision: 2,
      attempt: 1,
    },
  ]);
  expect(scans).toBe(1);

  const prior = snapshot.jobs[1];
  const updatedCommand = ['go', 'test', './internal/worker'];
  prior.evidence!.tests[1].status = 'passed';
  prior.evidence!.tests[1].checkoutSha = headSha;
  await panel.getByRole('tab', { name: 'Settings' }).click();
  await openRepositorySettings(panel, workerRepo);
  await openPolicySection(panel, 'VM tests');
  await command.fill(updatedCommand.join('\n'));
  await panel.getByRole('button', { name: 'Save settings' }).click();
  await expect(
    panel.getByRole('status').getByText('Settings saved'),
  ).toBeVisible();
  await inbox.click();
  const updated = vmTest.getByRole('button', { name: 'Run updated test' });
  await expect(updated).toBeEnabled();
  await expect(
    vmTest
      .getByRole('group', { name: 'Test command', exact: true })
      .getByText('./internal/queue'),
  ).toBeVisible();
  await expect(
    vmTest
      .getByRole('group', { name: 'Saved command for the next attempt' })
      .getByText('./internal/worker'),
  ).toBeVisible();
  await updated.click();
  await expect(vmTest.getByText('Attempt 3', { exact: true })).toBeVisible();
  await expect(updated).toHaveCount(0);
  await expect(
    vmTest
      .getByRole('group', { name: 'Test command', exact: true })
      .getByText('./internal/worker'),
  ).toBeVisible();
  await vmTest.getByText('Attempt 2 · Passed').click();
  await expect(
    vmTest
      .getByRole('group', { name: 'Command for attempt 2' })
      .getByText('./internal/queue'),
  ).toBeVisible();
  expect(testRetries[1]).toEqual({
    repository: repositories[0],
    number: 5,
    headSha,
    baseSha,
    revision: 3,
    attempt: 2,
  });
  prior.status = 'stale';
  prior.evidence!.checks.runs!.push(
    { name: 'Packaging', status: 'in_progress' },
    { name: 'Acceptance', status: 'completed', conclusion: 'failure' },
  );
  prior.evidence!.checks.status = 'failed';
  prior.evidence!.tests[2] = {
    attempt: 3,
    command: updatedCommand,
    status: 'passed',
    log: 'PASS TestWorkerRelease',
  };
  snapshot.jobs.unshift({
    ...prior,
    headSha: 'd'.repeat(40),
    status: 'queued',
    evidence: undefined,
  });
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(
    panel.getByText('This revision is superseded.', { exact: false }),
  ).toBeVisible();
  await showStageDetails(panel, 'Upstream CI');
  await showStageDetails(panel, 'Local quick test');
  await expect(
    checks.getByText('1 failed · 1 pending · 6 passed · 1 skipped'),
  ).toBeVisible();
  await expect(checks.getByText('Acceptance')).toBeVisible();
  await expect(checks.getByText('Packaging')).toBeVisible();
  await expect(checks.getByText('7 more check runs')).toBeVisible();
  await expect(retryTest).toHaveCount(0);
  await expect(vmTest.getByText('PASS TestWorkerRelease')).toBeHidden();
  await vmTest
    .locator('summary')
    .filter({ hasText: 'Test output for attempt 3' })
    .click();
  await expect(vmTest.getByText('PASS TestWorkerRelease')).toBeVisible();
  await goBack.click();
  await panel.getByRole('button', { name: 'History', exact: true }).click();
  await expect(
    panel
      .getByRole('region', { name: 'Superseded', exact: true })
      .getByRole('button', {
        name: 'Open example/worker #5 at aaaaaaaaaaaa',
      }),
  ).toBeVisible();
  await panel.getByRole('button', { name: 'In progress', exact: true }).click();
  await panel
    .getByRole('button', {
      name: 'Open example/worker #5 at dddddddddddd',
    })
    .click();
  await showEvidence(panel);
  await expect(vmTest.getByText('Waiting for review')).toBeVisible();

  await panel.getByRole('tab', { name: 'Settings' }).click();
  await openRepositorySettings(panel, workerRepo);
  await panel
    .getByRole('button', { name: 'Stop watching', exact: true })
    .click();
  await page
    .getByRole('dialog', { name: `Stop watching ${workerRepo}?` })
    .getByRole('button', { name: 'Stop watching', exact: true })
    .click();
  await expect(configureRepository(panel, workerRepo)).toHaveCount(0);
  expect(saved).toHaveLength(2);
  await panel.getByRole('button', { name: 'Save settings' }).click();
  await expect(
    panel.getByRole('status').getByText('Settings saved'),
  ).toBeVisible();
  expect(saved[2].repositoryPolicies).toEqual({
    [repositories[1]]: apiPolicy,
  });
});

test('shows upstream and quick test checkout evidence independently', async ({
  page,
}, testInfo) => {
  const repository = 'example/agent-api';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const mergeSha = 'c'.repeat(40);
  const updatedAt = '2026-11-19T07:00:00Z';
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 7,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: false },
    budget: { used: 2, resetsAt: '2026-11-20T00:00:00Z' },
    jobs: [
      {
        repository,
        number: 174,
        title: 'Exercise server startup',
        headSha,
        baseSha,
        status: 'analyzed',
        source: 'repository',
        updatedAt,
        evidence: {
          policy: {
            rules: 'Check deployment compatibility.',
            testCommand: ['python3', 'test_configure.py'],
          },
          checks: {
            status: 'passed',
            runs: [
              {
                name: 'Server smoke test',
                status: 'completed',
                conclusion: 'success',
                executionSha: mergeSha,
                executionRef: 'refs/pull/174/merge',
                scope: 'Start the server and exercise the client',
                provenanceUrl:
                  'https://github.com/example/agent-api/actions/runs/700/job/80#step:3:1',
              },
              { name: 'Lint', status: 'completed', conclusion: 'success' },
            ],
          },
          tests: [
            {
              status: 'passed',
              attempt: 1,
              taskId: 'task-1',
              attemptId: 'attempt-1',
              log: '7 tests passed',
            },
          ],
        },
      },
    ],
  };
  let refreshed = false;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    if (route.request().method() === 'POST') {
      expect(new URL(route.request().url()).pathname).toBe(
        '/api/github-review/refresh-checks',
      );
      expect(route.request().postDataJSON()).toEqual({
        repository,
        number: 174,
        headSha,
        baseSha,
        revision: 7,
      });
      refreshed = true;
    }
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
  await panel.getByRole('tab', { name: 'Pull requests' }).click();
  await panel
    .getByRole('button', {
      name: 'Open example/agent-api #174 at aaaaaaaaaaaa',
    })
    .click();
  const overview = panel.getByRole('region', { name: 'Review overview' });
  await expect(
    overview.getByText('Candidate comparisons', { exact: true }),
  ).toHaveCount(0);
  await showEvidence(panel);
  const quick = panel.getByRole('region', { name: 'Local quick test' });
  await expect(
    quick.getByText('Checkout unverified', { exact: true }),
  ).toBeVisible();
  const checkDetails = panel.getByRole('button', {
    name: 'Upstream CI details',
  });
  await expect(checkDetails).toContainText('1 without checkout');
  await expect(checkDetails).toHaveAttribute('aria-expanded', 'false');
  const upstream = await showStageDetails(panel, 'Upstream CI');
  await expect(
    upstream.getByText('Checks associated with PR head'),
  ).toContainText('aaaaaaaaaaaa');
  await expect(
    upstream.getByText('Start the server and exercise the client'),
  ).toBeVisible();
  await expect(
    upstream.getByText('Checkout cccccccccccc · merge commit'),
  ).toBeVisible();
  await expect(upstream.getByText('Checkout unknown')).toBeVisible();
  await expect(
    upstream.getByRole('link', { name: 'Checkout evidence' }),
  ).toHaveAttribute('href', /\/job\/80#step:3:1$/);
  await upstream.getByRole('button', { name: 'Refresh checks' }).click();
  await expect(panel.getByRole('status')).toHaveText('Check refresh requested');
  expect(refreshed).toBe(true);
  await expect(quick.getByText('Target aaaaaaaaaaaa · PR head')).toBeVisible();
  await expect(quick.getByText('Checkout unconfirmed')).toBeVisible();
  snapshot.jobs[0].evidence!.tests[0].checkoutSha = headSha;
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(quick.getByText('Passed', { exact: true })).toBeVisible();
  await showStageDetails(panel, 'Local quick test');
  await expect(
    quick.getByText('Checkout aaaaaaaaaaaa · PR head'),
  ).toBeVisible();
  await expect(quick.getByText('7 tests passed')).toBeVisible();
  const screenshot = testInfo.outputPath('review-checkouts.png');
  await panel.screenshot({ path: screenshot });
  await testInfo.attach('Upstream and local checkout provenance', {
    path: screenshot,
    contentType: 'image/png',
  });
});

test('assesses a candidate against selected evidence and tracks the issue on a later head', async ({
  page,
}, testInfo) => {
  const repository = 'example/agent-api';
  const headSha = 'a'.repeat(40),
    baseSha = 'b'.repeat(40),
    nextHead = 'c'.repeat(40);
  const account = 'workstation-user';
  const recorded = '2026-11-19T10:00:00Z';
  const candidate = {
    id: 'candidate-174',
    trackId: 'issue-pr174',
    index: 0,
    status: 'pending' as 'pending' | 'confirmed' | 'false_positive',
    note: '',
    revision: 1,
    evidence: [] as EvidenceItem[],
    history: [] as CandidateOccurrence[],
  };
  const previous: CandidateOccurrence = {
    id: 'older-174',
    headSha: '8'.repeat(40),
    baseSha,
    title: 'Check existing configuration',
    path: 'deploy/service/config.yaml',
    status: 'pending',
    revision: 1,
    note: '',
    evidence: [],
  };
  const localFailure: EvidenceItem = {
    id: 'test-compatibility',
    kind: 'test',
    status: 'failed',
    sha: headSha,
    label: 'Configuration compatibility',
    detail:
      'Existing configuration failed to load.\nexit 1 · required boolean missing',
  };
  const checkSuccess: EvidenceItem = {
    id: 'check-smoke',
    kind: 'check',
    status: 'passed',
    sha: headSha,
    label: 'Server smoke tests',
    detail: 'The server started with the default configuration.',
  };
  const snapshot: ReviewSnapshot = {
    config: {
      revision: 4,
      repositories: [repository],
      publishRepositories: [],
      enabled: false,
      dailyLimit: 10,
      intervalMinutes: 10,
      repositoryPolicies: {},
    },
    github: { connected: true, available: true, account },
    codex: { connected: true, available: true },
    jobs: [
      {
        repository,
        number: 174,
        headSha,
        baseSha,
        mergeBaseSha: '7'.repeat(40),
        source: 'repository',
        status: 'analyzed',
        updatedAt: recorded,
        title: 'Extend the server configuration',
        findings: [
          {
            title: 'Existing configuration can fail to load',
            body: 'Load settings written by a previous release.',
            path: 'deploy/service/config.yaml',
            line: 80,
            severity: 'medium',
          },
        ],
        candidates: [candidate],
        evidenceOptions: [localFailure, checkSuccess],
        previousCandidates: [previous],
        evidence: {
          policy: { testCommand: [], rules: 'Verify upgrades.' },
          checks: {
            status: 'passed',
            updatedAt: recorded,
            error: 'Provider timed out.',
            refreshErrorAt: '2026-11-19T10:30:00Z',
            runs: [
              {
                name: 'Upstream smoke tests',
                status: 'completed',
                conclusion: 'success',
              },
            ],
          },
          tests: [],
        },
      },
    ],
    budget: { used: 2, resetsAt: '2026-11-20T00:00:00Z' },
  };
  let conflict = true;
  let payload: CandidateDecision | undefined;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const req = route.request();
    if (req.method() === 'POST' && req.url().endsWith('/review-latest')) {
      const { repository, number, baseSha, headSha, revision } =
        req.postDataJSON();
      expect({ repository, number, baseSha, headSha, revision }).toEqual({
        repository: snapshot.jobs[0].repository,
        number: 174,
        baseSha: snapshot.jobs[0].baseSha,
        headSha: nextHead,
        revision: 4,
      });
      const previousJob = snapshot.jobs[0];
      previousJob.status = 'stale';
      const origin = previousJob.candidates![0].history![0];
      const newHead = 'd'.repeat(40);
      snapshot.jobs.unshift({
        ...previousJob,
        headSha: newHead,
        candidates: [],
        findings: [],
        previousCandidates: [origin],
        status: 'clean',
        evidenceOptions: [
          {
            ...localFailure,
            id: 'test-compatibility-fixed',
            sha: newHead,
            status: 'passed',
            detail: 'Compatibility test passed.',
          },
        ],
      });
      return route.fulfill({
        json: {
          ...snapshot,
          jobs: [],
          selectedReview: {
            repository,
            number,
            baseSha,
            headSha: newHead,
            status: 'clean',
          },
        },
      });
    }
    if (req.method() === 'POST' && req.url().endsWith('/candidate-followup')) {
      const latest = snapshot.jobs[0],
        origin = latest.previousCandidates![0];
      expect(req.postDataJSON()).toEqual({
        repository,
        number: 174,
        baseSha,
        headSha: latest.headSha,
        previousCandidateId: origin.id,
        account,
      });
      latest.followups = [
        {
          ...snapshot.jobs[2].findings![0],
          id: 'follow-174',
          trackId: candidate.trackId,
          index: -1,
          status: 'pending',
          note: '',
          revision: 1,
          evidence: [],
          history: [origin],
          originId: origin.id,
          originHeadSha: origin.headSha,
          originBaseSha: origin.baseSha,
        },
      ];
      return route.fulfill({ json: snapshot });
    }
    if (req.method() === 'POST') {
      expect(new URL(req.url()).pathname).toBe('/api/github-review/candidate');
      const body = req.postDataJSON() as CandidateDecision;
      if (conflict) {
        conflict = false;
        candidate.revision++;
        candidate.note = 'Someone else checked the default values.';
        return route.fulfill({
          status: 409,
          json: { error: 'The candidate decision changed.' },
        });
      }
      payload = body;
      const job = snapshot.jobs.find((j) => j.headSha === body.headSha)!;
      const active = [...(job.candidates ?? []), ...(job.followups ?? [])].find(
        (c) => c.id === body.candidateId,
      )!;
      expect(body.revision).toBe(active.revision);
      Object.assign(active, {
        status: body.status,
        note: body.note,
        revision: active.revision + 1,
        evidence: job.evidenceOptions!.filter((item) =>
          body.evidenceIds.includes(item.id),
        ),
      });
      if (body.previousCandidateId === previous.id) active.history = [previous];
    }
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
  const open = panel.getByRole('button', {
    name: 'Open example/agent-api #174 at aaaaaaaaaaaa',
  });
  await expect(
    panel.getByRole('region', { name: 'Needs action', exact: true }),
  ).toContainText('1 to assess');
  await open.click();
  const findings = panel.getByRole('region', {
    name: 'Local candidate comments',
  });
  const ci = panel.getByRole('region', { name: 'Upstream CI' });
  await showEvidence(panel);
  await expect(ci).toContainText('Showing the last recorded observation.');
  await expect(
    ci.getByText('Upstream smoke tests', { exact: true }),
  ).toBeVisible();
  await panel.getByText('Commits and review rules', { exact: true }).click();
  await expect(panel.getByText('7'.repeat(40), { exact: true })).toBeVisible();
  await findings.getByRole('button', { name: 'Assess candidate' }).click();
  await findings
    .getByRole('combobox', { name: 'Decision', exact: true })
    .selectOption('confirmed');
  await findings
    .getByLabel('Reason or next check')
    .fill(
      'Reproduced with the previous configuration. The defaults still work.',
    );
  await findings
    .getByLabel('Link evidence from this revision')
    .selectOption(localFailure.id);
  await findings
    .getByLabel('Link evidence from this revision')
    .selectOption(checkSuccess.id);
  await findings
    .getByLabel('Track the same issue across revisions')
    .selectOption(previous.id);
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  await open.click();
  await expect(
    findings.getByRole('combobox', { name: 'Decision', exact: true }),
  ).toHaveValue('confirmed');
  await findings.getByRole('button', { name: 'Save assessment' }).click();
  await expect(
    findings.getByText('The saved assessment changed.', { exact: false }),
  ).toBeVisible();
  await expect(
    findings.getByRole('button', { name: 'Save assessment' }),
  ).toBeDisabled();
  await findings
    .getByRole('button', { name: 'Keep my edits on this revision' })
    .click();
  await findings.getByRole('button', { name: 'Save assessment' }).click();
  await expect(findings.getByText('Confirmed', { exact: true })).toBeVisible();
  expect(payload).toEqual({
    repository,
    number: 174,
    headSha,
    baseSha,
    account,
    candidateId: candidate.id,
    revision: 2,
    status: 'confirmed',
    note: candidate.note,
    evidenceIds: [localFailure.id, checkSuccess.id],
    previousCandidateId: previous.id,
  });
  await findings.getByText('Candidate evidence', { exact: true }).click();
  await findings.getByText(localFailure.label, { exact: true }).click();
  await expect(
    findings.getByText('required boolean missing', { exact: false }),
  ).toBeVisible();
  await findings.getByText(/Issue history/).click();
  await expect(
    findings.getByText(previous.title, { exact: true }),
  ).toBeVisible();
  await findings.getByText(/Issue history/).click();
  await findings.evaluate((el) => el.scrollIntoView({ block: 'start' }));
  const shot = testInfo.outputPath('candidate-assessment.png');
  await panel.screenshot({ path: shot });
  await testInfo.attach('Local assessment and linked evidence', {
    path: shot,
    contentType: 'image/png',
  });

  const first = snapshot.jobs[0];
  first.status = 'stale';
  const newer = {
    ...candidate,
    id: 'new-candidate-174',
    revision: 1,
    status: 'pending' as const,
    note: '',
    evidence: [],
    history: [
      {
        id: candidate.id,
        title: first.findings![0].title,
        path: first.findings![0].path,
        baseSha,
        headSha,
        status: candidate.status,
        note: candidate.note,
        revision: candidate.revision,
        evidence: candidate.evidence,
      },
    ],
  };
  snapshot.jobs.unshift({
    ...first,
    headSha: nextHead,
    status: 'analyzed',
    candidates: [newer],
    evidenceOptions: [],
    previousCandidates: [],
  });
  await panel.getByRole('button', { name: 'Refresh review status' }).click();
  await expect(
    panel.getByText('This revision is superseded.', { exact: false }),
  ).toBeVisible();
  await expect(
    findings.getByRole('button', { name: 'Edit assessment' }),
  ).toHaveCount(0);
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  await panel
    .getByRole('button', {
      name: 'Open example/agent-api #174 at cccccccccccc',
    })
    .click();
  await expect(
    findings.getByText('Needs verification', { exact: true }),
  ).toBeVisible();
  await findings.getByText('Candidate evidence', { exact: true }).click();
  await findings.getByText(/Issue history/).click();
  await expect(
    findings.getByText(candidate.note, { exact: true }),
  ).toBeVisible();
  await findings.getByRole('button', { name: 'Assess candidate' }).click();
  await findings
    .getByRole('combobox', { name: 'Decision', exact: true })
    .selectOption('false_positive');
  await findings.getByRole('button', { name: 'Save assessment' }).click();
  await expect(
    findings.getByText('False positive', { exact: true }),
  ).toBeVisible();
  await showStageDetails(panel, 'Codex review');
  await panel
    .getByRole('button', { name: 'Review latest commit', exact: true })
    .click();
  await expect(panel.locator('.github-review-pr-header')).toContainText(
    'dddddddddddd',
  );
  await findings.getByText(/Earlier findings on this PR/).click();
  await expect(findings.getByText('Confirmed', { exact: true })).toBeVisible();
  await findings
    .getByRole('button', { name: 'Follow up on this revision' })
    .click();
  await expect(findings).toContainText('Reviewer follow-up');
  await expect(findings).toContainText(
    'Location on original head aaaaaaaaaaaa',
  );
  await findings.getByRole('button', { name: 'Assess candidate' }).click();
  await findings
    .getByRole('combobox', { name: 'Decision', exact: true })
    .selectOption('resolved');
  await findings
    .getByLabel('Link evidence from this revision')
    .selectOption('test-compatibility-fixed');
  await findings.getByRole('button', { name: 'Save assessment' }).click();
  await expect(
    findings.getByText('Resolved on this revision', { exact: true }),
  ).toBeVisible();
  await findings.getByText('Candidate evidence', { exact: true }).click();
  await expect(
    findings.getByRole('group', { name: 'Linked evidence' }),
  ).toContainText('Compatibility test passed.');
  expect(candidate.status).toBe('confirmed');
  expect(snapshot.budget.used).toBe(2);
});

test('orders repository tests and reruns one PR with an auditable selection', async ({
  page,
}, testInfo) => {
  const repository = 'example/agent-api';
  const otherRepository = 'example/docs';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const policy = { rules: '', testCommand: ['go', 'test', './sdk/...'] };
  const snapshot: ReviewSnapshot = {
    config: {
      repositoryPolicies: { [repository]: policy },
      repositories: [repository, otherRepository],
      publishRepositories: [],
      enabled: true,
      intervalMinutes: 5,
      dailyLimit: 10,
      revision: 2,
    },
    github: { available: true, connected: true, account: 'reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 1, resetsAt: '2026-11-20T00:00:00Z' },
    jobs: [
      {
        repository,
        number: 174,
        baseSha,
        headSha,
        source: 'repository',
        status: 'analyzed',
        updatedAt: '2026-11-19T12:00:00Z',
        evidence: {
          policy,
          checks: { status: 'passed' },
          tests: [],
          review: {
            status: 'completed',
            authorContext: {
              title: 'Document server deployment',
              description: 'Verify <gateway> readiness.\nThe SDK is unchanged.',
              descriptionTruncated: true,
            },
          },
        },
      },
      {
        repository,
        number: 175,
        baseSha,
        headSha: 'c'.repeat(40),
        source: 'repository',
        status: 'analyzed',
        title: 'Update the server configuration',
        updatedAt: '2026-11-19T13:00:00Z',
        evidence: {
          policy,
          review: { status: 'completed' },
          checks: { status: 'passed' },
          tests: [
            {
              attempt: 1,
              status: 'passed',
              selection: 'automatic',
              selectionBaseSha: 'd'.repeat(40),
              testName: 'SDK',
              selectionReason: 'Matched sdk in the previous comparison.',
              command: policy.testCommand,
              taskId: 'earlier-task',
              attemptId: 'earlier-attempt',
            },
          ],
        },
      },
    ],
  };
  const saves: ReviewConfig[] = [];
  const retries: (ReviewRetry & { attempt: number; testId?: string })[] = [];
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === '/api/github-review/config') {
      const data = request.postDataJSON() as ReviewConfig;
      saves.push(data);
      snapshot.config = { ...data, revision: data.revision + 1 };
    } else if (path === '/api/github-review/retry-test') {
      const data = request.postDataJSON() as (typeof retries)[number];
      retries.push(data);
      const profiles =
        snapshot.config.repositoryPolicies![repository].testProfiles!;
      const chosen =
        profiles.find((profile) => data.testId === profile.id) ?? profiles[0];
      const skipped = data.number === 174 && !data.testId;
      const job = snapshot.jobs.find((item) => item.number === data.number)!;
      job.evidence!.tests.push({
        attempt: data.attempt + 1,
        status: skipped ? 'not_applicable' : 'passed',
        selection: data.testId ? 'manual' : 'automatic',
        selectionBaseSha: data.baseSha,
        selectionReason: skipped
          ? 'No changed path matches the test profiles.'
          : data.testId
            ? 'Selected for this request; entry file exists in the PR head.'
            : 'Matched deploy/config and found the entry file in the PR head.',
        ...(!skipped
          ? {
              testName: chosen.name,
              testId: chosen.id,
              entryPath: chosen.entryPath,
              command: chosen.command,
              taskId: 'quick-vm-task',
              attemptId: 'vm-attempt',
              checkoutSha: data.headSha,
            }
          : {}),
      });
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
  await panel.getByRole('tab', { name: 'Settings' }).click();
  await openRepositorySettings(panel, repository);
  await openPolicySection(panel, 'VM tests');
  const setup = panel.getByRole('region', { name: 'Quick tests' });
  const add = setup.getByRole('button', { name: 'Add test profile' });
  const entry = setup.getByLabel('Required entry file');
  const save = panel.getByRole('button', {
    name: 'Save settings',
    exact: true,
  });
  const command = setup.getByRole('textbox', {
    name: 'Test command (one argument per line)',
    exact: true,
  });
  await add.click();
  await setup.getByLabel('Test name').fill('SDK');
  await setup
    .getByLabel('Changed files or directories (one per line)')
    .fill('sdk');
  await expect(command).toHaveValue(policy.testCommand.join('\n'));
  await openRepositorySettings(panel, otherRepository);
  await expect(
    panel.getByRole('heading', { name: otherRepository }),
  ).toBeVisible();
  await save.click();
  await expect(panel.getByRole('heading', { name: repository })).toBeVisible();
  await expect(entry).toBeFocused();
  await expect(setup.getByLabel('Test name')).toHaveValue('SDK');
  await expect(save).toBeInViewport();
  expect(saves).toHaveLength(0);
  await entry.fill('sdk/go.mod');
  await add.click();
  await setup.getByLabel('Test name').fill('Configuration checks');
  await setup
    .getByLabel('Required entry file')
    .fill('deploy/scripts/test_configure.py');
  await setup
    .getByLabel('Changed files or directories (one per line)')
    .fill('deploy/config\ndeploy/scripts');
  await command.fill('python3\ndeploy/scripts/test_configure.py');
  await setup.getByRole('button', { name: 'Move earlier' }).click();
  await expect(setup.getByLabel('Test order').locator('option')).toHaveText([
    '1. Configuration checks',
    '2. SDK',
  ]);
  await setup.evaluate((element) => element.scrollIntoView({ block: 'start' }));
  const settingsShot = testInfo.outputPath('repository-quick-tests.png');
  await panel.screenshot({ path: settingsShot });
  await testInfo.attach('Repository quick tests', {
    path: settingsShot,
    contentType: 'image/png',
  });
  await panel.getByText('Comparison runtimes', { exact: true }).click();
  await panel.getByRole('button', { name: 'Add runtime' }).click();
  await panel.getByLabel('Runtime name', { exact: true }).fill('Guest Python');
  await panel
    .getByLabel('Launcher (one argument per line)')
    .fill('python3\n-c');
  await panel.getByRole('button', { name: 'Save settings' }).click();
  await expect(
    panel.getByText('Settings saved', { exact: true }),
  ).toBeVisible();
  const saved = saves[0].repositoryPolicies![repository];
  expect(saved.comparisonRuntimes).toEqual([
    {
      id: expect.any(String),
      name: 'Guest Python',
      command: ['python3', '-c'],
    },
  ]);
  expect(saved.testCommand).toEqual(policy.testCommand);
  expect(saved.testProfiles).toMatchObject([
    {
      name: 'Configuration checks',
      entryPath: 'deploy/scripts/test_configure.py',
      appliesTo: ['deploy/config', 'deploy/scripts'],
      command: ['python3', 'deploy/scripts/test_configure.py'],
    },
    {
      name: 'SDK',
      entryPath: 'sdk/go.mod',
      appliesTo: ['sdk'],
      command: policy.testCommand,
    },
  ]);
  await panel.getByRole('tab', { name: 'Pull requests' }).click();
  await panel
    .getByRole('button', {
      name: 'Open example/agent-api #174 at aaaaaaaaaaaa',
    })
    .click();
  await showStageDetails(panel, 'Codex review');
  await panel
    .getByText('Author context used for review', { exact: true })
    .click();
  await expect(
    panel.getByText('Verify <gateway> readiness.', { exact: false }),
  ).toBeVisible();
  await expect(
    panel.getByText('The review used an excerpt of the author’s description.'),
  ).toBeVisible();
  const stage = await showStageDetails(panel, 'Local quick test');
  const select = stage.getByLabel('Next test for this PR');
  const auto = stage.getByRole('button', { name: 'Run automatic selection' });
  await auto.click();
  await expect(
    stage.getByText('Not applicable', { exact: true }),
  ).toBeVisible();
  await showStageDetails(panel, 'Local quick test');
  await expect(
    stage.getByText('No changed path matches the test profiles.'),
  ).toBeVisible();
  await expect(stage.getByText('Target aaaaaaaaaaaa · PR head')).toBeVisible();
  await select.selectOption({ label: 'SDK' });
  await stage.getByRole('button', { name: 'Run selected test' }).click();
  await expect(
    stage.getByText('Selected for this PR', { exact: false }),
  ).toContainText('sdk/go.mod');
  await expect(
    stage.getByText('Checkout aaaaaaaaaaaa · PR head'),
  ).toBeVisible();
  await stage.getByText('Test command', { exact: true }).click();
  await expect(
    stage
      .getByRole('group', { name: 'Test command', exact: true })
      .locator('code'),
  ).toHaveText(policy.testCommand);
  await stage.getByText('Attempt 1 · Not applicable').click();
  await expect(
    stage.getByText('No changed path matches the test profiles.'),
  ).toBeVisible();
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  const staleQuickTest = panel.getByRole('button', {
    name: 'Open example/agent-api #175 at cccccccccccc',
  });
  await expect(staleQuickTest).toContainText('Earlier comparison');
  await expect(staleQuickTest).not.toContainText('Quick test: Passed');
  await staleQuickTest.click();
  await showStageDetails(panel, 'Local quick test');
  await expect(select).toHaveValue('');
  await expect(
    stage.getByText('Earlier comparison', { exact: true }),
  ).toBeVisible();
  await expect(
    stage.getByText(
      'Selected against dddddddddddd. Current review base bbbbbbbbbbbb.',
    ),
  ).toBeVisible();
  await auto.click();
  await expect(stage.getByText('Passed', { exact: true })).toBeVisible();
  await showStageDetails(panel, 'Local quick test');
  await expect(
    stage.getByText(
      'Matched deploy/config and found the entry file in the PR head.',
    ),
  ).toBeVisible();
  await expect(stage.getByText('Selection base bbbbbbbbbbbb')).toBeVisible();
  await expect(
    stage.getByText('Attempt 1 · Passed · SDK · Earlier comparison'),
  ).toBeVisible();
  await stage.evaluate((element) => element.scrollIntoView({ block: 'start' }));
  const selectionShot = testInfo.outputPath('quick-test-selection.png');
  await panel.screenshot({ path: selectionShot });
  await testInfo.attach('Quick test selection', {
    path: selectionShot,
    contentType: 'image/png',
  });
  const identity = { repository, number: 174, baseSha, headSha, revision: 3 };
  expect(retries).toEqual([
    { ...identity, attempt: 0 },
    { ...identity, attempt: 1, testId: saved.testProfiles![1].id },
    { ...identity, number: 175, headSha: 'c'.repeat(40), attempt: 1 },
  ]);
  expect(saves).toHaveLength(1);
});

test('keeps unsaved settings when a newer saved revision is observed', async ({
  page,
}) => {
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: ['example/current'],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 10,
      enabled: false,
      revision: 1,
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 0, resetsAt: '2026-11-22T00:00:00Z' },
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
  await panel.getByRole('tab', { name: 'Settings', exact: true }).click();
  const interval = panel.getByLabel('Poll interval in minutes');
  const save = panel.getByRole('button', {
    name: 'Save settings',
    exact: true,
  });
  await expect(interval).toHaveValue('10');
  await expect(save).toHaveCount(0);
  await panel
    .getByRole('button', { name: 'Add repository', exact: true })
    .click();
  const repositoryName = panel.getByLabel('Repository name', { exact: true });
  await repositoryName.fill('missing-owner');
  await repositoryName.press('Enter');
  await expect(repositoryName).toHaveAttribute('aria-invalid', 'true');
  await expect(save).toHaveCount(0);
  await repositoryName.fill('example/staged');
  await repositoryName.press('Enter');
  await expect(configureRepository(panel, 'example/staged')).toBeFocused();
  await interval.fill('25');
  await expect(save).toBeEnabled();
  expect(snapshot.config.repositories).toEqual(['example/current']);

  snapshot.config = {
    ...snapshot.config,
    repositories: ['example/current', 'example/remote'],
    intervalMinutes: 15,
    revision: 2,
  };
  const reload = panel.getByRole('button', {
    name: 'Reload settings',
    exact: true,
  });
  await expect(reload).toBeVisible();
  await expect(save).toHaveCount(0);
  await expect(interval).toHaveValue('25');
  await expect(configureRepository(panel, 'example/staged')).toBeVisible();
  await reload.click();
  const confirmation = page.getByRole('dialog', {
    name: 'Discard review settings?',
  });
  await confirmation
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(interval).toHaveValue('25');
  await reload.click();
  await confirmation
    .getByRole('button', { name: 'Discard settings', exact: true })
    .click();
  await expect(interval).toHaveValue('15');
  await expect(configureRepository(panel, 'example/staged')).toHaveCount(0);
  await expect(configureRepository(panel, 'example/remote')).toBeVisible();
  await expect(save).toHaveCount(0);
});

test('compares a candidate on both recorded checkouts with one assertion and one request ID', async ({
  page,
}, testInfo) => {
  const repository = 'demo/library';
  const headSha = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const mergeBaseSha = 'c'.repeat(40);
  const now = '2026-11-19T09:00:00Z';
  const runtime = {
    id: 'py311',
    name: 'Python 3.11',
    command: ['/opt/deps/py311', '-c'],
  };
  const snapshot: ReviewSnapshot = {
    github: { available: true, connected: true, account: 'reviewer' },
    codex: { available: true, connected: true },
    comparisonReady: true,
    budget: { used: 2, resetsAt: now },
    config: {
      enabled: false,
      revision: 3,
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 5,
      repositoryPolicies: {
        [repository]: {
          rules: '',
          testCommand: [],
          comparisonRuntimes: [
            { id: 'sh', name: 'Shell', command: ['sh', '-ec'] },
            runtime,
          ],
        },
      },
    },
    jobs: [
      {
        repository,
        number: 2,
        headSha,
        baseSha,
        mergeBaseSha,
        title: 'Maintain an initializer',
        updatedAt: now,
        status: 'analyzed',
        source: 'repository',
        findings: [
          {
            path: 'lib.py',
            line: 10,
            title: 'Review first construction',
            severity: 'medium',
            body: 'Check the initialization path.',
          },
        ],
        candidates: [
          {
            id: 'candidate-two',
            trackId: 'tracking',
            index: 0,
            status: 'pending',
            note: '',
            revision: 1,
          },
        ],
      },
    ],
  };
  const job = snapshot.jobs[0],
    candidate = job.candidates![0];
  const assertion = 'import sys\nassert sys.version_info.major == 3';
  const ids: string[] = [];
  const detailReads: string[] = [];
  let failRead = true;
  await page.route('**/api/github-review**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (
      req.method() === 'GET' &&
      url.pathname.includes('/candidate-comparison/')
    ) {
      const id = url.pathname.split('/').at(-1)!;
      detailReads.push(id);
      const result = candidate.comparisons?.find((run) => run.id === id);
      if (!result)
        return route.fulfill({ status: 404, json: { error: 'Unknown run' } });
      if (failRead) {
        return route.fulfill({
          status: 503,
          json: { error: 'Logs temporarily unavailable.' },
        });
      }
      return route.fulfill({ json: result });
    }
    if (req.method() === 'POST') {
      expect(new URL(req.url()).pathname).toBe(
        '/api/github-review/candidate-comparison',
      );
      const body = req.postDataJSON();
      ids.push(body.requestId);
      expect(body).toEqual({
        account: 'reviewer',
        repository,
        number: 2,
        baseSha,
        headSha,
        candidateId: candidate.id,
        revision: 3,
        requestId: expect.stringMatching(/^[\da-f-]{36}$/),
        runtimeId: runtime.id,
        assertion,
      });
      if (ids.length === 1)
        return route.fulfill({
          status: 503,
          json: { error: 'Comparison response unavailable.' },
        });
      candidate.comparisons = [
        {
          id: body.requestId,
          candidateId: candidate.id,
          repository,
          number: 2,
          baseSha,
          headSha,
          mergeBaseSha,
          runtime,
          assertion,
          status: 'running',
          createdAt: now,
          updatedAt: now,
          baseline: {
            status: 'running',
            targetSha: mergeBaseSha,
            checkoutSha: mergeBaseSha,
            log: 'Baseline started',
          },
          head: { status: 'queued', targetSha: headSha },
        },
      ];
    }
    const summary = structuredClone(snapshot);
    for (const entry of summary.jobs[0].candidates![0].comparisons ?? []) {
      delete entry.assertion;
      delete entry.baseline.log;
      delete entry.head.log;
    }
    if (await answerReviewRead(route, summary)) return;
    return route.fulfill({ json: summary });
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
    .getByRole('button', {
      name: 'Open demo/library #2 at ' + headSha.slice(0, 12),
    })
    .click();
  const finding = panel.getByRole('region', {
    name: 'Local candidate comments',
  });
  const candidateEvidence = finding.getByText('Candidate evidence', {
    exact: true,
  });
  await candidateEvidence.click();
  await finding
    .getByRole('button', { name: 'Compare revisions', exact: true })
    .click();
  const form = finding.getByRole('form', { name: 'Compare revisions' });
  const run = form.getByRole('button', { name: 'Run comparison', exact: true });
  await expect(run).toBeDisabled();
  await form.getByLabel('Runtime', { exact: true }).selectOption(runtime.id);
  await form.getByLabel('Assertion', { exact: true }).fill(assertion);
  await run.click();
  await expect(
    panel.getByText('Comparison response unavailable.'),
  ).toBeVisible();
  await expect(form.getByLabel('Assertion', { exact: true })).toHaveValue(
    assertion,
  );
  await run.click();
  await expect(form).toHaveCount(0);
  expect(ids).toEqual([ids[0], ids[0]]);
  const evidence = finding.getByRole('region', { name: 'Comparison evidence' });
  const ancestor = evidence.getByRole('region', {
    name: 'Common ancestor',
    exact: true,
  });
  const head = evidence.getByRole('region', { name: 'PR head', exact: true });
  const details = evidence.locator(
    ':scope > .github-review-comparison-details',
  );
  const logs = details.getByText('Logs and execution details', { exact: true });
  const ancestorDetails = details.getByRole('region', {
    name: 'Common ancestor execution details',
  });
  const headDetails = details.getByRole('region', {
    name: 'PR head execution details',
  });
  await expect(head).toContainText('Checkout pending');
  await expect(evidence.getByText('Comparing revisions')).toBeVisible();
  await expect.poll(() => detailReads.length).toBeGreaterThan(0);
  await expect(details).not.toHaveAttribute('open', '');
  await logs.click();
  await expect(
    evidence.getByText('Logs temporarily unavailable.'),
  ).toBeVisible();
  failRead = false;
  await evidence.getByRole('button', { name: 'Retry evidence' }).click();
  await expect(ancestorDetails).toContainText('Baseline started');
  await expect(
    finding.getByRole('button', { name: 'Compare revisions', exact: true }),
  ).toHaveCount(0);
  const result = candidate.comparisons![0];
  Object.assign(result, {
    updatedAt: '2026-11-19T09:01:00Z',
    status: 'completed',
    outcome: 'head_only_failed',
  });
  Object.assign(result.baseline, {
    status: 'passed',
    assertionExitCode: 0,
    log: 'baseline OK',
    taskId: 'ancestor-task',
  });
  Object.assign(result.head, {
    status: 'failed',
    checkoutSha: headSha,
    assertionExitCode: 1,
    exitCode: 10,
    log: 'first construction raised TypeError',
    taskId: 'head-task',
  });
  candidate.comparisons!.push({
    ...structuredClone(result),
    id: 'earlier-run',
    createdAt: '2026-11-19T08:00:00Z',
    updatedAt: '2026-11-19T08:01:00Z',
    outcome: 'inconclusive',
  });
  await expect(
    evidence.getByText('Assertion failed only on PR head'),
  ).toBeVisible();
  await expect(ancestor).toContainText(mergeBaseSha.slice(0, 12));
  await expect(ancestor).toContainText('Assertion exit 0');
  await expect(head).toContainText(headSha.slice(0, 12));
  await expect(head).toContainText('Assertion exit 1');
  await expect(ancestorDetails.locator('dd')).toContainText([
    mergeBaseSha,
    mergeBaseSha,
    '0',
  ]);
  await expect(headDetails.locator('dd')).toContainText([
    headSha,
    headSha,
    '1',
  ]);
  await expect(ancestorDetails.getByLabel('Common ancestor output')).toHaveText(
    'baseline OK',
  );
  await expect(headDetails.getByLabel('PR head output')).toHaveText(
    'first construction raised TypeError',
  );
  const panes = await Promise.all([ancestor.boundingBox(), head.boundingBox()]);
  const available = await evidence.evaluate((el) => el.clientWidth);
  if (available > 490) expect(panes[1]!.x).toBeGreaterThan(panes[0]!.x + 10);
  else expect(panes[1]!.y).toBeGreaterThan(panes[0]!.y + 10);
  await evidence.getByText('Recorded assertion and runtime').click();
  await expect(evidence.getByText(assertion, { exact: true })).toBeVisible();
  await logs.click();
  await evidence.scrollIntoViewIfNeeded();
  await expect(evidence).toBeInViewport({ ratio: 0 });
  await evidence.screenshot({
    path: testInfo.outputPath('candidate-comparison.png'),
  });
  await expect(finding.locator('span[data-decision="pending"]')).toBeVisible();
  await expect(
    finding.getByText('Needs assessment', { exact: true }),
  ).toBeVisible();
  await finding
    .getByRole('button', { name: 'Compare revisions', exact: true })
    .click();
  await expect(form.getByLabel('Assertion', { exact: true })).toHaveValue(
    assertion,
  );
  await expect(form.getByLabel('Runtime', { exact: true })).toHaveValue(
    runtime.id,
  );
  await form.getByRole('button', { name: 'Cancel' }).click();
  expect(new Set(detailReads)).toEqual(new Set([ids[0]]));
  const readsBeforeReopen = detailReads.length;
  await panel.getByRole('button', { name: '← Pull requests' }).click();
  await panel
    .getByRole('button', {
      name: 'Open demo/library #2 at ' + headSha.slice(0, 12),
    })
    .click();
  await expect(candidateEvidence.locator('..')).not.toHaveAttribute('open', '');
  const resultSummary = finding.locator('.github-review-candidate-result');
  await expect(resultSummary).toContainText('Assertion failed only on PR head');
  await resultSummary.getByRole('button', { name: 'View comparison' }).click();
  await expect(
    evidence.getByText('Assertion failed only on PR head'),
  ).toBeVisible();
  await expect(evidence).toBeFocused();
  await expect(resultSummary).toBeHidden();
  await expect(details).not.toHaveAttribute('open', '');
  await expect(headDetails.getByLabel('PR head output')).toHaveCount(0);
  expect(detailReads).toHaveLength(readsBeforeReopen);
  await evidence.getByText('Earlier comparisons · 1').click();
  const earlier = evidence.getByText(/Python 3.11 · Comparison inconclusive/);
  await earlier.click();
  const earlierLogs = earlier
    .locator('..')
    .getByText('Logs and execution details', { exact: true });
  await expect(earlierLogs).toBeVisible();
  expect(detailReads).toHaveLength(readsBeforeReopen);
  await earlierLogs.click();
  await expect.poll(() => detailReads.at(-1)).toBe('earlier-run');
  expect(detailReads).toHaveLength(readsBeforeReopen + 1);
  await earlier.click();
  await logs.click();
  job.status = 'stale';
  await expect(resultSummary).toHaveCount(0);
  await expect(
    finding.getByRole('button', { name: 'Compare revisions', exact: true }),
  ).toHaveCount(0);
  await expect(headDetails.getByLabel('PR head output')).toBeVisible();
});
