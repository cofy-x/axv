import type { Page } from '@playwright/test';
import { actionMenu, expect, menuItem, persistedLayout, test } from './helpers';
import {
  codeEditor,
  editVMFile,
  expectEditorText,
} from './code-editor-helpers';
import type {
  GitHubAssociationState,
  GitHubObservation,
  GitHubRepository,
} from '../apps/dashboard/src/features/project-files/vm-github-api';
import type { VMProjectBinding } from '../apps/dashboard/src/features/project-files/vm-api';

const project = {
  id: 'github-project',
  hostId: 'github-host',
  kind: 'vm',
  name: 'GitHub repository',
  volumeId: 'github-volume',
  root: 'repo',
  rootIdentity: 'github-root',
  revision: 1,
  createdAt: '2026-11-06T00:00:00Z',
};
const originalBinding: VMProjectBinding = {
  hostId: project.hostId,
  projectId: project.id,
  projectRevision: 1,
  volumeId: project.volumeId,
  volumeRevision: 2,
  machineId: 'github-machine',
  bootId: 'github-boot',
  instanceId: 'github-instance',
};
const windowId = `project-files:vm:${project.hostId}:${project.id}`;
const browser = (page: Page) => page.locator(`[data-window-id="${windowId}"]`);
const github = (page: Page) =>
  browser(page).getByRole('region', {
    name: 'Project GitHub view',
    exact: true,
  });
const repository = (name = 'owner/repository'): GitHubRepository => ({
  id: `id:${name}`,
  nameWithOwner: name,
  url: `https://github.com/${name}`,
});
const account = { id: 'account-id', login: 'reviewed-user' };
const sha = '1111111111111111111111111111111111111111';

function observation(repo = repository()): GitHubObservation {
  const check = {
    name: 'Unit tests',
    source: 'check-run',
    status: 'completed',
    conclusion: 'success',
    url: `${repo.url}/actions/runs/1`,
  };
  const checks = {
    repository: repo,
    sha,
    state: 'success',
    complete: true,
    checks: [check],
  };
  return {
    git: {
      repository: true,
      branch: 'feature/review',
      headSha: sha,
      detached: false,
      unborn: false,
      remotes: [{ name: 'origin', repository: repo.nameWithOwner }],
      remotesPartial: false,
    },
    account,
    repository: repo,
    localChecks: checks,
    pullRequests: [
      {
        id: 'pr-one',
        number: 12,
        title: 'Reviewed change',
        url: `${repo.url}/pull/12`,
        state: 'open',
        baseRepository: repo,
        headRepository: repo,
        headBranch: 'feature/review',
        headSha: sha,
        headChecks: checks,
        mergeChecks: {
          ...checks,
          sha: '2222222222222222222222222222222222222222',
        },
      },
      {
        id: 'pr-fork',
        number: 13,
        title: 'Fork candidate',
        url: `${repo.url}/pull/13`,
        state: 'open',
        baseRepository: repo,
        headRepository: repository('fork/repository'),
        headBranch: 'feature/review',
        headSha: '3333333333333333333333333333333333333333',
        headChecks: {
          ...checks,
          repository: repository('fork/repository'),
          sha: '3333333333333333333333333333333333333333',
        },
      },
    ],
    pullRequestsComplete: true,
    observedAt: '2026-11-06T01:00:00Z',
  };
}

function deferredRequest() {
  let received!: () => void;
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    received = resolve;
  });
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { received, pending, gate, release };
}

async function fixture(page: Page, associated = true) {
  let binding = { ...originalBinding };
  let state: GitHubAssociationState = {
    revision: associated ? 1 : 0,
    ...(associated
      ? {
          association: {
            repository: repository(),
            account,
            machineId: binding.machineId,
            profile: 'axv' as const,
            updatedAt: project.createdAt,
          },
        }
      : {}),
  };
  const requests: {
    action: string;
    binding: VMProjectBinding;
    revision: number;
    repository?: string;
    repositoryId?: string;
    accountId?: string;
  }[] = [];
  let taskRequests = 0;
  let bindingRequests = 0;
  let authentication = true;
  let failure = '';
  let next = observation();
  let delayed: ReturnType<typeof deferredRequest> | undefined;
  let delayedBinding: ReturnType<typeof deferredRequest> | undefined;
  let delayedAssociation: ReturnType<typeof deferredRequest> | undefined;
  await page.route('**/api/compute/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (value: unknown, status = 200) =>
      route.fulfill({ status, json: value });
    if (path === '/api/compute/info')
      return json({
        configured: true,
        hostId: project.hostId,
        providers: [],
        images: [],
      });
    if (path === '/api/compute/machines') return json({ machines: [] });
    if (path === '/api/compute/volumes') return json({ volumes: [] });
    if (path === '/api/compute/projects') return json({ projects: [project] });
    if (!path.startsWith(`/api/compute/projects/${project.id}/`))
      return route.continue();
    expect(route.request().headers()['x-axv-host-id']).toBe(project.hostId);
    if (path.endsWith('/binding')) {
      bindingRequests++;
      if (delayedBinding) {
        const wait = delayedBinding;
        delayedBinding = undefined;
        wait.received();
        await wait.gate;
      }
      return json({ project, binding });
    }
    if (path.endsWith('/files/list'))
      return json({
        project,
        binding,
        entries: [
          {
            name: 'main.ts',
            path: 'main.ts',
            kind: 'file',
            size: 10,
            modifiedAt: project.createdAt,
          },
        ],
      });
    if (path.endsWith('/files/read'))
      return json({
        project,
        binding,
        path: 'main.ts',
        content: 'Saved code\n',
        sha256: 'file-sha',
        size: 11,
        modifiedAt: project.createdAt,
      });
    if (path.includes('/tasks') || path.includes('/task-requests')) {
      taskRequests++;
      return json(
        {
          code: 'task_capacity',
          error: 'All retained task receipts are occupied.',
        },
        409,
      );
    }
    if (path.endsWith('/github')) {
      if (delayedAssociation) {
        const wait = delayedAssociation;
        delayedAssociation = undefined;
        wait.received();
        await wait.gate;
      }
      return json(structuredClone(state));
    }
    const action = path.split('/').at(-1)!;
    const input = route.request().postDataJSON();
    requests.push({ action, ...input });
    if (JSON.stringify(input.binding) !== JSON.stringify(binding))
      return json(
        {
          code: 'binding_changed',
          error: 'The original VM binding changed. Reconnect explicitly.',
        },
        409,
      );
    if (input.revision !== state.revision)
      return json(
        {
          code: 'github_association_changed',
          error: 'The GitHub association changed.',
        },
        409,
      );
    if (
      (action === 'review' || action === 'associate' || action === 'refresh') &&
      !authentication
    )
      return json(
        {
          code: 'github_authentication_required',
          error: 'Authenticate the dedicated guest AXV GitHub profile.',
        },
        401,
      );
    if (action === 'associate') {
      expect(input.repositoryId).toBe(repository(input.repository).id);
      expect(input.accountId).toBe(account.id);
      state = {
        revision: state.revision + 1,
        association: {
          repository: repository(input.repository),
          account,
          machineId: binding.machineId,
          profile: 'axv',
          updatedAt: project.createdAt,
        },
      };
      next = observation(state.association!.repository);
      return json(state);
    }
    if (action === 'clear') {
      state = { revision: state.revision + 1 };
      return json(state);
    }
    if (action === 'refresh') {
      expect(Object.keys(input).sort()).toEqual(['binding', 'revision']);
      if (failure)
        return json({ code: 'github_rate_limited', error: failure }, 429);
    }
    const result = {
      binding: { ...binding },
      revision: state.revision,
      observation:
        action === 'review'
          ? observation(repository(input.repository))
          : structuredClone(next),
    };
    if (action === 'refresh' && delayed) {
      const wait = delayed;
      delayed = undefined;
      wait.received();
      await wait.gate;
    }
    return json(result);
  });
  return {
    requests,
    get tasks() {
      return taskRequests;
    },
    get bindings() {
      return bindingRequests;
    },
    setAuthentication(value: boolean) {
      authentication = value;
    },
    setFailure(value: string) {
      failure = value;
    },
    setObservation(value: GitHubObservation) {
      next = value;
    },
    replaceVM() {
      binding = {
        ...originalBinding,
        machineId: 'replacement-vm',
        bootId: 'replacement-boot',
        instanceId: 'replacement-instance',
        volumeRevision: 3,
      };
    },
    delayBinding() {
      delayedBinding = deferredRequest();
      return delayedBinding;
    },
    delayAssociation() {
      delayedAssociation = deferredRequest();
      return delayedAssociation;
    },
    delayRefresh() {
      delayed = deferredRequest();
      return delayed;
    },
  };
}

async function open(page: Page) {
  await page
    .getByRole('button', { name: 'Workspace menu', exact: true })
    .click();
  await menuItem(page, 'Applications').click();
  await menuItem(page, 'Project Files').click();
  const launcher = page.locator('[data-window-id="project-files:projects"]');
  await launcher.getByRole('tab', { name: 'VM', exact: true }).click();
  await launcher.getByRole('button', { name: /^GitHub repository ·/ }).click();
  await browser(page).getByRole('tab', { name: 'GitHub', exact: true }).click();
  await expect(
    github(page).getByRole('button', {
      name: 'Read Git metadata',
      exact: true,
    }),
  ).toBeEnabled();
}

async function associate(page: Page, name = 'owner/repository') {
  await github(page)
    .getByRole('textbox', { name: 'GitHub repository', exact: true })
    .fill(name);
  await github(page)
    .getByRole('button', { name: 'Review repository and account', exact: true })
    .click();
  await expect(
    github(page).getByRole('region', {
      name: 'Repository review',
      exact: true,
    }),
  ).toContainText('reviewed-user');
  await github(page)
    .getByRole('button', { name: 'Associate reviewed repository', exact: true })
    .click();
  await expect(
    github(page)
      .getByRole('status')
      .filter({ hasText: 'Repository associated.' }),
  ).toBeVisible();
}

test('GitHub requires explicit reviewed association and distinguishes local, PR and merge checks', async ({
  page,
  request,
}) => {
  const state = await fixture(page, false);
  await open(page);
  expect(state.requests).toHaveLength(0);
  await expect(
    github(page).getByRole('button', { name: 'Refresh GitHub', exact: true }),
  ).toBeDisabled();
  await github(page)
    .getByRole('button', { name: 'Read Git metadata', exact: true })
    .click();
  await github(page)
    .getByRole('button', { name: 'origin · owner/repository', exact: true })
    .click();
  await associate(page);
  expect(
    state.requests.filter((item) => item.action === 'refresh'),
  ).toHaveLength(0);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(
    github(page).getByRole('link', {
      name: '#12 Reviewed change',
      exact: true,
    }),
  ).toHaveAttribute('href', 'https://github.com/owner/repository/pull/12');
  await expect(
    github(page).getByRole('link', { name: '#13 Fork candidate', exact: true }),
  ).toBeVisible();
  await expect(
    github(page).getByRole('region', {
      name: 'Local HEAD checks',
      exact: true,
    }),
  ).toContainText(sha);
  await expect(
    github(page).getByRole('region', {
      name: 'PR #12 merge checks · separate SHA',
      exact: true,
    }),
  ).toContainText('2222222222222222222222222222222222222222');
  await expect(github(page)).toContainText('PR head does not match local HEAD');
  expect(state.tasks).toBe(0);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (item: { id: string }) => item.id === windowId,
        )?.viewState.projectSection,
    )
    .toBe('github');
  await page.reload();
  await expect(
    github(page).getByRole('region', {
      name: 'Reviewed GitHub association',
      exact: true,
    }),
  ).toContainText('owner/repository');
  await expect(
    github(page).getByRole('region', {
      name: 'GitHub observation',
      exact: true,
    }),
  ).toHaveCount(0);
  expect(
    state.requests.filter((item) => item.action === 'refresh'),
  ).toHaveLength(1);
});

test('GitHub surfaces dedicated-profile authentication and retains stale results on rate limits', async ({
  page,
}) => {
  const state = await fixture(page, false);
  state.setAuthentication(false);
  await open(page);
  await github(page)
    .getByRole('textbox', { name: 'GitHub repository', exact: true })
    .fill('owner/repository');
  await github(page)
    .getByRole('button', { name: 'Review repository and account', exact: true })
    .click();
  await expect(github(page).getByRole('alert')).toContainText(
    'dedicated guest AXV GitHub profile',
  );
  await github(page)
    .getByText('Set up guest GitHub access', { exact: true })
    .click();
  await expect(github(page)).toContainText(
    'GH_CONFIG_DIR="$HOME/.config/axv/github"',
  );
  await expect(github(page)).toContainText('--insecure-storage');
  await expect(github(page)).toContainText('stores that token as plaintext');
  state.setAuthentication(true);
  await associate(page);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(
    github(page).getByRole('link', {
      name: '#12 Reviewed change',
      exact: true,
    }),
  ).toBeVisible();
  state.setFailure('GitHub API rate limit reached. Try again after the reset.');
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(github(page).getByRole('alert')).toContainText('rate limit');
  await expect(
    github(page).getByRole('heading', {
      name: 'Stale GitHub observation',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    github(page).getByRole('link', {
      name: '#12 Reviewed change',
      exact: true,
    }),
  ).toBeVisible();
  expect(state.tasks).toBe(0);
});

test('a restored GitHub tab reloads its association when binding arrives during the settings read', async ({
  page,
  request,
}) => {
  const state = await fixture(page);
  await open(page);
  await expect
    .poll(
      async () =>
        (await persistedLayout(page, request))?.windows.find(
          (item: { id: string }) => item.id === windowId,
        )?.viewState.projectSection,
    )
    .toBe('github');
  const binding = state.delayBinding();
  const association = state.delayAssociation();
  try {
    await page.reload();
    await Promise.all([binding.pending, association.pending]);
    binding.release();
    await expect(browser(page).locator('.project-location')).toContainText(
      originalBinding.machineId,
    );
    association.release();
    await expect(
      github(page).getByRole('region', {
        name: 'Reviewed GitHub association',
        exact: true,
      }),
    ).toContainText('owner/repository');
    await expect(
      github(page).getByRole('button', {
        name: 'Read Git metadata',
        exact: true,
      }),
    ).toBeEnabled();
    await expect(
      github(page).getByRole('button', {
        name: 'Refresh GitHub',
        exact: true,
      }),
    ).toBeEnabled();
    expect(state.requests).toHaveLength(0);
    expect(state.tasks).toBe(0);
  } finally {
    binding.release();
    association.release();
  }
});

test('a delayed GitHub response cannot replace a newly reviewed repository', async ({
  page,
}) => {
  const state = await fixture(page);
  await open(page);
  const delayed = state.delayRefresh();
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await delayed.pending;
  await browser(page).getByRole('tab', { name: 'Files', exact: true }).click();
  await expect(
    browser(page).getByRole('button', { name: 'GitHub Status', exact: true }),
  ).toHaveCount(0);
  await browser(page).getByRole('tab', { name: 'GitHub', exact: true }).click();
  await expect(
    github(page).getByRole('button', {
      name: 'Read Git metadata',
      exact: true,
    }),
  ).toBeEnabled();
  await associate(page, 'other/repository');
  delayed.release();
  await expect(
    github(page).getByRole('region', {
      name: 'Reviewed GitHub association',
      exact: true,
    }),
  ).toContainText('other/repository');
  await expect(
    github(page).getByRole('region', {
      name: 'GitHub observation',
      exact: true,
    }),
  ).toHaveCount(0);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(
    github(page).getByRole('region', {
      name: 'GitHub observation',
      exact: true,
    }),
  ).toContainText('other/repository');
  expect(state.tasks).toBe(0);
});

test('replacement VM requires explicit reconnect and association review', async ({
  page,
}) => {
  const state = await fixture(page);
  await open(page);
  state.replaceVM();
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(github(page).getByRole('alert')).toContainText(
    'original VM binding changed',
  );
  expect(state.bindings).toBe(1);
  await browser(page).getByRole('tab', { name: 'Files', exact: true }).click();
  await actionMenu(page, browser(page), 'More VM project actions', 'Reconnect');
  await expect.poll(() => state.bindings).toBe(2);
  await browser(page).getByRole('tab', { name: 'GitHub', exact: true }).click();
  await expect(github(page)).toContainText('reviewed on another VM');
  await expect(
    github(page).getByRole('button', { name: 'Refresh GitHub', exact: true }),
  ).toBeDisabled();
  await associate(page);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(
    github(page).getByRole('region', {
      name: 'GitHub observation',
      exact: true,
    }),
  ).toBeVisible();
  expect(state.requests.at(-1)?.binding.machineId).toBe('replacement-vm');
});

test('GitHub stays bounded on narrow screens and preserves mounted dirty code', async ({
  page,
  isMobile,
}, testInfo) => {
  const state = await fixture(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  await browser(page).getByRole('tab', { name: 'Files', exact: true }).click();
  await browser(page)
    .getByRole('button', { name: 'main.ts', exact: true })
    .click();
  const editor = await editVMFile(browser(page));
  await editor.fill('Unsaved code remains here\n');
  await browser(page).getByRole('tab', { name: 'GitHub', exact: true }).click();
  await expect(codeEditor(browser(page))).not.toBeEditable();
  const value = observation();
  value.git.detached = true;
  value.git.branch = '';
  value.localChecks = { ...value.localChecks!, sha: 'obsolete-sha' };
  state.setObservation(value);
  const refreshButton = github(page).getByRole('button', {
    name: 'Refresh GitHub',
    exact: true,
  });
  if (isMobile) await refreshButton.tap();
  else {
    await refreshButton.focus();
    await refreshButton.press('Enter');
  }
  await expect(github(page)).toContainText('Detached HEAD');
  await expect(
    github(page).getByRole('region', {
      name: 'Local HEAD checks',
      exact: true,
    }),
  ).toContainText('Unmatched repository or commit');
  expect(
    await github(page).evaluate(
      (element) => element.scrollWidth <= element.clientWidth + 1,
    ),
  ).toBe(true);
  const screenshot = testInfo.outputPath('vm-project-github-narrow.png');
  await browser(page).screenshot({ path: screenshot });
  await testInfo.attach('Narrow GitHub panel', {
    path: screenshot,
    contentType: 'image/png',
  });
  await browser(page).getByRole('tab', { name: 'Files', exact: true }).click();
  await expectEditorText(editor, 'Unsaved code remains here\n');
  await expect(editor).toBeEditable();
  expect(state.tasks).toBe(0);
});

test('advancing PR heads and unborn branches never inherit another commit’s completed checks', async ({
  page,
}) => {
  const state = await fixture(page);
  await open(page);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(github(page)).toContainText('PR head matches local HEAD.');
  const advanced = observation();
  advanced.pullRequests[0].headSha = '4444444444444444444444444444444444444444';
  advanced.localChecks = { ...advanced.localChecks!, complete: false };
  state.setObservation(advanced);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(
    github(page).getByRole('region', {
      name: 'PR #12 head checks',
      exact: true,
    }),
  ).toContainText('Unmatched repository or commit');
  await expect(
    github(page).getByRole('region', {
      name: 'Local HEAD checks',
      exact: true,
    }),
  ).toContainText('Incomplete checks');
  await expect(
    github(page).getByText('PR head matches local HEAD.', { exact: true }),
  ).toHaveCount(0);
  const unborn = observation();
  unborn.git.unborn = true;
  delete unborn.git.headSha;
  unborn.localChecks = undefined;
  unborn.pullRequests = [];
  state.setObservation(unborn);
  await github(page)
    .getByRole('button', { name: 'Refresh GitHub', exact: true })
    .click();
  await expect(github(page)).toContainText('no commit yet');
  await expect(
    github(page).getByRole('region', {
      name: 'Local HEAD checks',
      exact: true,
    }),
  ).toContainText('No checks observed');
  await expect(
    github(page).getByText('Checks completed', { exact: true }),
  ).toHaveCount(0);
  expect(state.tasks).toBe(0);
});
