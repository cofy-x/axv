import type {
  Candidate,
  CandidateComparison,
  CandidateCommentDraft,
  ReviewJob,
  ReviewSnapshot,
} from '../apps/dashboard/src/features/github-review/api';
import { expect, menuItem, test } from './helpers';
import { answerReviewRead } from './github-review-fixtures';

test('preflights the selected repository VM without saving or starting a task', async ({
  page,
}) => {
  const repository = 'example/docs';
  const snapshot: ReviewSnapshot = {
    config: {
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 10,
      dailyLimit: 3,
      enabled: false,
      revision: 2,
      repositoryPolicies: {},
    },
    repositoryEnvironments: {
      [repository]: { name: 'Documentation VM', configured: true },
    },
    github: { available: true, connected: true, account: 'fixture-reviewer' },
    codex: { available: true, connected: true },
    budget: { used: 0, resetsAt: '2026-11-21T00:00:00Z' },
    jobs: [],
  };
  const blocked =
    'Start the registered review VM and wait for its guest to become ready.';
  const reachable =
    'The review VM and project are reachable. The test will check its commit and dependencies when it runs.';
  let probes = 0;
  await page.route('**/api/github-review**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    expect(request.method()).toBe('GET');
    if (url.pathname.endsWith('/vm-readiness')) {
      expect(url.searchParams.get('repository')).toBe(repository);
      probes++;
      await route.fulfill({
        json:
          probes === 1
            ? { status: 'guest_unready', message: blocked }
            : { status: 'reachable', message: reachable },
      });
      return;
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
  await panel.getByRole('tab', { name: 'Settings', exact: true }).click();
  await panel.getByRole('button', { name: 'Configure ' + repository }).click();
  expect(probes).toBe(0);
  await panel
    .getByText('VM tests', { exact: false })
    .filter({ hasText: 'Documentation VM' })
    .click();
  await expect(panel.getByText(blocked, { exact: true })).toBeVisible();
  await panel.getByRole('button', { name: 'Recheck VM' }).click();
  await expect(panel.getByText(reachable, { exact: true })).toBeVisible();
  expect(probes).toBe(2);
});

test('approves one exact candidate scenario and keeps automatic evidence and drafts revision-scoped', async ({
  page,
}) => {
  const repository = 'example/worker';
  const title = 'Change worker initialization';
  const account = 'fixture-reviewer';
  const now = '2026-11-21T03:00:00Z';
  const initialHead = 'a'.repeat(40);
  const baseSha = 'b'.repeat(40);
  const laterHead = 'c'.repeat(40);
  const changedBase = 'd'.repeat(40);
  const finding = {
    title: 'Preserve the worker initializer',
    path: 'src/worker.go',
    line: 25,
    side: 'head' as const,
    severity: 'medium',
    body: 'A caller can receive a worker before initialization completes.',
  };
  const runtime = {
    id: 'shell',
    name: 'POSIX shell',
    command: ['/bin/sh', '-ec'],
  };
  const binding = {
    hostId: 'host-review',
    machineId: 'vm-review',
    bootId: 'boot-review',
    projectId: 'project-review',
    projectRevision: 1,
    volumeId: 'volume-review',
    volumeRevision: 1,
    instanceId: 'instance-review',
  };
  const sourceId = 'e'.repeat(64);
  const nextId = 'f'.repeat(64);
  const missingId = '8'.repeat(64);
  const setupRunId = '11111111-1111-4111-8111-111111111111';
  const automaticRunId = '22222222-2222-4222-8222-222222222222';
  const scenario = {
    id: '1'.repeat(64),
    name: finding.title,
    title: finding.title,
    path: finding.path,
    side: finding.side,
    sourceComparisonId: setupRunId,
  };
  const originalDraft = 'Existing manually reviewed draft.';
  const proposedDraft =
    'The approved test reproduced this difference on the current PR head.';

  function comparison(
    id: string,
    candidateId: string,
    head: string,
  ): CandidateComparison {
    return {
      id,
      candidateId,
      repository,
      number: 12,
      baseSha,
      headSha: head,
      mergeBaseSha: baseSha,
      runtime,
      assertion: 'test -s src/worker.go',
      status: 'completed',
      outcome: 'head_only_failed',
      baseline: {
        status: 'passed',
        targetSha: baseSha,
        checkoutSha: baseSha,
        assertionExitCode: 0,
        exitCode: 0,
        binding,
        taskId: 'task-base-' + id,
        attemptId: 'attempt-base-' + id,
        log: 'Original initializer loaded',
      },
      head: {
        status: 'failed',
        targetSha: head,
        checkoutSha: head,
        assertionExitCode: 1,
        exitCode: 10,
        binding,
        taskId: 'task-head-' + id,
        attemptId: 'attempt-head-' + id,
        log: 'Worker returned before initialization',
      },
      createdAt: now,
      updatedAt: now,
    };
  }

  function comment(body: string, head: string): CandidateCommentDraft {
    return {
      body,
      revision: 1,
      updatedAt: now,
      baseSha,
      headSha: head,
      anchorSha: head,
      path: finding.path,
      line: finding.line,
      side: finding.side,
      stale: false,
    };
  }

  const source: Candidate = {
    id: sourceId,
    trackId: sourceId,
    index: 0,
    revision: 1,
    status: 'confirmed',
    note: 'The original comparison was reviewed manually.',
    comparisons: [comparison(setupRunId, sourceId, initialHead)],
    commentDraft: comment(originalDraft, initialHead),
  };
  const current: Candidate = {
    id: nextId,
    trackId: sourceId,
    index: 0,
    revision: 1,
    status: 'pending',
    note: '',
    autoValidation: {
      state: 'queued',
      reason: 'The approved scenario is queued.',
      scenarioId: scenario.id,
      scenarioName: scenario.name,
      comparisonId: automaticRunId,
    },
  };
  const unmatched: Candidate = {
    id: missingId,
    trackId: missingId,
    index: 1,
    revision: 1,
    status: 'pending',
    note: '',
    autoValidation: {
      state: 'needs_review',
      reason: 'No approved scenario matches this finding.',
    },
  };
  const unfamiliar = {
    ...finding,
    title: 'Handle an unrelated closing path',
    line: 28,
  };
  const original: ReviewJob = {
    repository,
    number: 12,
    title,
    source: 'repository',
    baseSha,
    headSha: initialHead,
    mergeBaseSha: baseSha,
    status: 'analyzed',
    updatedAt: now,
    findings: [finding],
    candidates: [source],
    evidence: {
      review: { status: 'completed' },
      policy: { rules: '', testCommand: [] },
      checks: { status: 'passed' },
      tests: [],
    },
  };
  const next: ReviewJob = {
    ...original,
    headSha: laterHead,
    findings: [finding],
    candidates: [current],
  };
  const snapshot: ReviewSnapshot = {
    github: { available: true, connected: true, account },
    codex: { available: true, connected: true },
    comparisonReady: false,
    repositoryEnvironments: {
      [repository.toUpperCase()]: { name: 'Project test VM', configured: true },
    },
    budget: { used: 1, resetsAt: now },
    config: {
      enabled: false,
      revision: 1,
      repositories: [repository],
      publishRepositories: [],
      intervalMinutes: 5,
      dailyLimit: 10,
      repositoryPolicies: {
        [repository]: {
          rules: '',
          testCommand: [],
          comparisonRuntimes: [runtime],
        },
      },
    },
    jobs: [original],
  };
  let writes = 0;
  await page.route('**/api/github-review**', async (route) => {
    if (await answerReviewRead(route, snapshot)) return;
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === 'POST') {
      expect(path).toBe('/api/github-review/approve-comparison-scenario');
      expect(request.postDataJSON()).toEqual({
        account,
        repository,
        number: 12,
        baseSha,
        headSha: initialHead,
        revision: 1,
        candidateId: sourceId,
        comparisonId: setupRunId,
        name: finding.title,
        enable: true,
      });
      expect(++writes).toBe(1);
      snapshot.config.revision++;
      const policy = snapshot.config.repositoryPolicies![repository];
      policy.autoCandidateValidation = true;
      policy.comparisonScenarios = [scenario];
      source.autoValidation = {
        state: 'verified',
        reason: 'The recorded comparison matches this review.',
        scenarioId: scenario.id,
        scenarioName: scenario.name,
        comparisonId: setupRunId,
        reused: true,
        suggestion: { status: 'confirmed' },
      };
    } else {
      expect(request.method()).toBe('GET');
      if (path.endsWith('/' + setupRunId))
        return route.fulfill({ json: source.comparisons![0] });
      if (path.endsWith('/' + automaticRunId)) {
        return route.fulfill({
          json: comparison(automaticRunId, nextId, laterHead),
        });
      }
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
  const open = (head: string) =>
    panel
      .getByRole('button', {
        name: `Open ${repository} #12 at ${head.slice(0, 12)}`,
        exact: true,
      })
      .click();
  const refresh = panel.getByRole('button', { name: 'Refresh review status' });
  const evidence = panel.getByRole('region', {
    name: 'Automatic verification',
    exact: true,
  });
  const candidateEvidence = panel.getByText('Candidate evidence', {
    exact: true,
  });
  const approval = panel.getByRole('button', {
    name: 'Use as automatic scenario',
    exact: true,
  });
  const manual = panel.getByRole('button', {
    name: 'Compare revisions',
    exact: true,
  });
  const draft = panel.getByText('Local comment draft', { exact: true });
  const back = panel.getByRole('button', {
    name: '← Pull requests',
    exact: true,
  });

  await open(initialHead);
  await candidateEvidence.click();
  await panel.getByText('Automatic scenario', { exact: true }).click();
  await expect(approval).toBeVisible();
  expect(writes).toBe(0);
  await approval.click();
  await expect(evidence).toContainText('Difference reproduced');
  await expect(evidence).toContainText('Suggested decision · Confirmed');
  await draft.click();
  await expect(panel.getByText(originalDraft, { exact: true })).toBeVisible();

  original.status = 'stale';
  source.autoValidation = {
    ...source.autoValidation!,
    state: 'stale',
    reason: 'The PR head changed.',
  };
  source.commentDraft!.stale = true;
  snapshot.jobs.unshift(next);
  await refresh.click();
  await expect(evidence).toContainText('Earlier revision');
  await expect(manual).toHaveCount(0);
  await expect(panel.getByText(originalDraft, { exact: true })).toBeVisible();
  await back.click();
  await panel.getByRole('button', { name: 'In progress', exact: true }).click();
  await expect(
    panel.getByRole('region', { name: 'In progress', exact: true }),
  ).toContainText(title);

  await open(laterHead);
  await expect(evidence).toContainText('Automatic verification queued');
  await candidateEvidence.click();
  current.autoValidation = {
    ...current.autoValidation!,
    state: 'running',
    reason: 'Running on the dedicated VM.',
  };
  await refresh.click();
  await expect(evidence).toContainText('Running automatic comparison');
  current.autoValidation = {
    ...current.autoValidation!,
    state: 'verified',
    reason: 'The comparison reproduced the difference.',
    suggestion: { status: 'confirmed' },
  };
  current.comparisons = [comparison(automaticRunId, nextId, laterHead)];
  current.commentDraft = comment(proposedDraft, laterHead);
  await refresh.click();
  await expect(evidence).toContainText('Difference reproduced');
  await expect(evidence).toContainText('Suggested decision · Confirmed');
  await draft.click();
  await expect(panel.getByText(proposedDraft, { exact: true })).toBeVisible();
  await expect(
    panel.locator(
      '.github-review-selected-candidate [data-decision="suggested"]',
    ),
  ).toHaveText('Draft to review');
  await expect(manual).toBeVisible();
  await expect(
    panel.getByRole('button', { name: 'Assess candidate', exact: true }),
  ).toBeHidden();
  await panel.getByText('View evidence', { exact: true }).click();
  await panel
    .getByRole('region', { name: 'Review overview', exact: true })
    .getByRole('button', { name: 'View comparison', exact: true })
    .click();
  const comparisonEvidence = panel.getByRole('region', {
    name: 'Comparison evidence',
    exact: true,
  });
  await expect(comparisonEvidence).toBeFocused();
  await expect(
    comparisonEvidence.getByRole('region', { name: 'Common ancestor' }),
  ).toContainText(baseSha.slice(0, 12));
  await expect(
    comparisonEvidence.getByRole('region', { name: 'PR head' }),
  ).toContainText(laterHead.slice(0, 12));
  await evidence.getByRole('button', { name: 'Review suggestion' }).click();
  const decision = panel.getByRole('combobox', {
    name: 'Decision',
    exact: true,
  });
  await expect(decision).toHaveValue('confirmed');
  await expect(
    panel.getByRole('textbox', { name: 'Reason or next check' }),
  ).toHaveValue(current.autoValidation.reason);
  await panel.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(decision).toHaveCount(0);
  expect(writes).toBe(1);

  await back.click();
  await panel
    .getByRole('button', { name: 'Needs action', exact: true })
    .click();
  await expect(
    panel.getByRole('region', { name: 'Needs action', exact: true }),
  ).toContainText(title);
  next.findings!.push(unfamiliar);
  next.candidates!.push(unmatched);
  await refresh.click();
  await open(laterHead);
  await panel
    .getByRole('button', { name: new RegExp(unfamiliar.title) })
    .click();
  await expect(evidence).toContainText('Needs your review');
  await expect(evidence).toContainText(
    'No approved scenario matches this finding.',
  );
  const compare = evidence.getByRole('button', {
    name: 'Compare revisions',
    exact: true,
  });
  await expect(compare).toBeVisible();
  await expect(
    panel.locator('details.github-review-candidate-evidence'),
  ).not.toHaveAttribute('open', '');
  await compare.click();
  const compareForm = panel.getByRole('form', {
    name: 'Compare revisions',
    exact: true,
  });
  await expect(
    compareForm.getByRole('textbox', { name: 'Assertion' }),
  ).toBeVisible();
  await compareForm
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  snapshot.comparisonReady = true;
  snapshot.repositoryEnvironments![repository.toUpperCase()].configured = false;
  await refresh.click();
  await expect(compare).toBeDisabled();
  await expect(evidence).toContainText(
    'Configure the dedicated review VM to run a comparison.',
  );
  expect(writes).toBe(1);
  await expect(panel.getByText(proposedDraft, { exact: true })).toHaveCount(0);
  await panel.getByRole('button', { name: new RegExp(finding.title) }).click();
  await expect(evidence).toContainText('Difference reproduced');

  next.status = 'stale';
  next.observedBaseSha = changedBase;
  current.autoValidation = {
    ...current.autoValidation!,
    state: 'stale',
    reason: 'The comparison base changed.',
  };
  current.commentDraft!.stale = true;
  await refresh.click();
  await expect(evidence).toContainText('Earlier revision');
  await expect(manual).toHaveCount(0);
  await expect(
    panel.getByText(/The comparison for this head changed/),
  ).toBeVisible();
  await draft.click();
  await expect(panel.getByText(proposedDraft, { exact: true })).toBeVisible();
  expect(writes).toBe(1);
});
