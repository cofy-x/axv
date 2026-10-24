import type { CloudflareConfig, CloudflareConfigWrite } from './types';

export type ConfigDraft = Omit<
  CloudflareConfigWrite,
  'instanceId' | 'expectedRevision'
>;

export function configDraft(config: CloudflareConfig): ConfigDraft {
  const { enabled: _enabled, ...access } = config.access;
  const {
    configured: _configured,
    hasToken: _hasToken,
    ...account
  } = config.account;
  return { access, account, tokenAction: 'keep', token: '' };
}

export function sameDraft(a: ConfigDraft, b: ConfigDraft) {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** A completed save acknowledges its snapshot, never subsequent typing. */
export function savedDraft(
  current: ConfigDraft,
  submitted: ConfigDraft,
  saved: CloudflareConfig,
) {
  return sameDraft(current, submitted) ? configDraft(saved) : current;
}

export function configWrite(
  config: CloudflareConfig,
  draft: ConfigDraft,
): CloudflareConfigWrite {
  return {
    instanceId: config.instanceId,
    expectedRevision: config.revision,
    access: { ...draft.access },
    account: { ...draft.account },
    tokenAction: draft.tokenAction,
    ...(draft.tokenAction === 'replace' ? { token: draft.token } : {}),
  };
}
