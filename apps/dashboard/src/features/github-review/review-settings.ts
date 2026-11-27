import type {
  ComparisonRuntime,
  ComparisonScenario,
  ReviewConfig,
  ReviewVMTarget,
  TestProfile,
} from './api';

export interface TestProfileDraft {
  id: string;
  name: string;
  entryPath: string;
  appliesTo: string;
  command: string;
}

export function testProfileDraft(profile: TestProfile): TestProfileDraft {
  return {
    ...profile,
    appliesTo: (profile.appliesTo ?? []).join('\n'),
    command: profile.command.join('\n'),
  };
}

export function testProfileConfig(profile: TestProfileDraft): TestProfile {
  const appliesTo = profile.appliesTo
    .split('\n')
    .map((path) => path.trim())
    .filter(Boolean);
  return {
    id: profile.id,
    name: profile.name.trim(),
    entryPath: profile.entryPath.trim(),
    ...(appliesTo.length ? { appliesTo } : {}),
    command: profile.command.split('\n').filter(Boolean),
  };
}

export interface PolicyDraft {
  rules: string;
  command: string;
  profiles: TestProfileDraft[];
  runtimes: ComparisonRuntime[];
  autoCandidateValidation: boolean;
  comparisonScenarios: ComparisonScenario[];
}

export interface SettingsDraft {
  watchOnly: boolean;
  repositories: string[];
  policies: Record<string, PolicyDraft>;
  vmTargets: Record<string, ReviewVMTarget>;
  intervalMinutes: string;
  dailyLimit: string;
  enabled: boolean;
  revision: number;
}

export type SettingsPatch = Partial<Omit<SettingsDraft, 'revision'>>;

export interface RepositorySettingsIssue {
  kind: 'rules' | 'quick-test' | 'runtime' | 'verification';
  message: string;
  field?: 'name' | 'entryPath' | 'command' | 'appliesTo';
  profileId?: string;
  runtimeId?: string;
}

export interface ScanSettingsIssue {
  field: 'intervalMinutes' | 'dailyLimit';
  message: string;
}

export type SettingsIssue =
  | { kind: 'schedule'; issue: ScanSettingsIssue }
  | { kind: 'repository'; repository: string; issue: RepositorySettingsIssue };

export const emptyPolicyDraft: PolicyDraft = {
  rules: '',
  command: '',
  profiles: [],
  runtimes: [],
  autoCandidateValidation: false,
  comparisonScenarios: [],
};

export const watchedRepositoryLimit = 100;

export function isWatchedRepositoryName(name: string) {
  const match = /^[a-z\d][a-z\d-]{0,38}\/([a-z\d_.-]{1,100})$/i.exec(name);
  return Boolean(match && match[1] !== '.' && match[1] !== '..');
}

export function configDraft(config: ReviewConfig): SettingsDraft {
  return {
    repositories: config.repositories ?? [],
    policies: Object.fromEntries(
      Object.entries(config.repositoryPolicies ?? {}).map(([repo, policy]) => [
        repo.toLowerCase(),
        {
          rules: policy.rules ?? '',
          command: policy.testCommand?.join('\n') ?? '',
          profiles: (policy.testProfiles ?? []).map(testProfileDraft),
          runtimes: policy.comparisonRuntimes ?? [],
          autoCandidateValidation: !!policy.autoCandidateValidation,
          comparisonScenarios: policy.comparisonScenarios ?? [],
        },
      ]),
    ),
    vmTargets: Object.fromEntries(
      Object.entries(config.repositoryVmTargets ?? {}).map(([repo, target]) => [
        repo.toLowerCase(),
        target,
      ]),
    ),
    intervalMinutes: String(config.intervalMinutes),
    dailyLimit: String(config.dailyLimit),
    enabled: config.enabled,
    watchOnly: Boolean(config.watchOnly),
    revision: config.revision,
  };
}

export function hasPolicy(policy: PolicyDraft) {
  return Boolean(
    policy.rules ||
    policy.command ||
    policy.profiles.length ||
    policy.runtimes.length ||
    policy.comparisonScenarios.length ||
    policy.autoCandidateValidation,
  );
}

function commandLines(value: string) {
  return value.split('\n').filter((arg) => arg.length > 0);
}

export function configFromDraft(draft: SettingsDraft): ReviewConfig {
  const repositoryPolicies = Object.fromEntries(
    draft.repositories.flatMap((repo) => {
      const policy = draft.policies[repo.toLowerCase()];
      if (!policy || !hasPolicy(policy)) return [];
      return [
        [
          repo,
          {
            rules: policy.rules,
            ...(policy.profiles.length
              ? { testProfiles: policy.profiles.map(testProfileConfig) }
              : {}),
            ...(policy.runtimes.length
              ? {
                  comparisonRuntimes: policy.runtimes.map((runtime) => ({
                    ...runtime,
                    name: runtime.name.trim(),
                    command: runtime.command.filter((arg) => arg.length > 0),
                  })),
                }
              : {}),
            ...(policy.autoCandidateValidation
              ? { autoCandidateValidation: true }
              : {}),
            testCommand: commandLines(policy.command),
          },
        ],
      ];
    }),
  );
  const repositoryVmTargets = Object.fromEntries(
    draft.repositories.flatMap((repo) => {
      const target = draft.vmTargets[repo.toLowerCase()];
      return target ? [[repo, target]] : [];
    }),
  );
  return {
    enabled: draft.enabled,
    ...(draft.watchOnly ? { watchOnly: true } : {}),
    revision: draft.revision,
    repositoryPolicies,
    ...(Object.keys(repositoryVmTargets).length ? { repositoryVmTargets } : {}),
    repositories: draft.repositories,
    publishRepositories: [],
    intervalMinutes: Number(draft.intervalMinutes),
    dailyLimit: Number(draft.dailyLimit),
  };
}

const encoder = new TextEncoder();
const controlCharacter = /\p{Cc}/u;
const commandFormat =
  'Use up to 32 nonblank arguments, one per line, with at most 512 UTF-8 bytes per argument.';

function validName(name: string) {
  return (
    !!name.trim() &&
    encoder.encode(name.trim()).length <= 80 &&
    !controlCharacter.test(name)
  );
}

function validCommand(command: string[]) {
  return (
    command.length <= 32 &&
    command.every(
      (arg) =>
        !!arg.trim() &&
        encoder.encode(arg).length <= 512 &&
        !controlCharacter.test(arg),
    )
  );
}

function validTestPath(path: string) {
  return (
    encoder.encode(path).length <= 512 &&
    !/[\\*?[\]]|\p{Cc}/u.test(path) &&
    path
      .split('/')
      .every(
        (part) =>
          part &&
          part !== '.' &&
          part !== '..' &&
          part.toLowerCase() !== '.git',
      )
  );
}

function invalidPolicy(
  policy: PolicyDraft,
): RepositorySettingsIssue | undefined {
  if (
    encoder.encode(policy.rules).length > 4096 ||
    controlCharacter.test(policy.rules.replace(/[\n\r\t]/g, ''))
  )
    return {
      kind: 'rules',
      message: 'Use up to 4,096 UTF-8 bytes of plain text for review rules.',
    };
  if (!validCommand(commandLines(policy.command)))
    return { kind: 'quick-test', field: 'command', message: commandFormat };

  for (const item of policy.profiles) {
    const profile = testProfileConfig(item);
    const target = { kind: 'quick-test', profileId: profile.id } as const;
    if (!validName(profile.name))
      return {
        ...target,
        field: 'name',
        message: 'Enter a test name of up to 80 UTF-8 bytes.',
      };
    if (!validTestPath(profile.entryPath))
      return {
        ...target,
        field: 'entryPath',
        message: 'Enter a file path relative to this repository.',
      };
    if (!profile.command.length || !validCommand(profile.command))
      return {
        ...target,
        field: 'command',
        message: 'Enter a test command. ' + commandFormat,
      };
    const scopes = profile.appliesTo ?? [];
    if (
      scopes.length > 16 ||
      scopes.some((scope) => !validTestPath(scope.replace(/\/$/, '')))
    )
      return {
        ...target,
        field: 'appliesTo',
        message: 'Enter up to 16 repository-relative file or directory paths.',
      };
  }
  for (const runtime of policy.runtimes) {
    const target = { kind: 'runtime', runtimeId: runtime.id } as const;
    const command = runtime.command.filter((arg) => arg.length > 0);
    if (!validName(runtime.name.trim()))
      return {
        ...target,
        field: 'name',
        message: 'Enter a runtime name of up to 80 UTF-8 bytes.',
      };
    if (!command.length || !validCommand(command))
      return {
        ...target,
        field: 'command',
        message: 'Enter a launcher. ' + commandFormat,
      };
  }
}

export function invalidSettings(
  draft: SettingsDraft,
): SettingsIssue | undefined {
  for (const [field, max, unit] of [
    ['intervalMinutes', 1440, 'scan interval in minutes'],
    ['dailyLimit', 1000, 'daily Codex review limit'],
  ] as const) {
    const value = Number(draft[field]);
    if (!Number.isInteger(value) || value < 1 || value > max)
      return {
        kind: 'schedule',
        issue: {
          field,
          message:
            'Enter a ' + unit + ' from 1 to ' + max.toLocaleString() + '.',
        },
      };
  }
  for (const repository of draft.repositories) {
    const policy = draft.policies[repository.toLowerCase()];
    const issue = policy && invalidPolicy(policy);
    if (issue) return { kind: 'repository', repository, issue };
  }
}
