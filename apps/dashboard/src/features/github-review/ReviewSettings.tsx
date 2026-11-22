import { useEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import type { ReviewProvider, ReviewSnapshot } from './api';
import { reviewEnvironment } from './api';
import { ReviewConnections, ReviewScanSettings } from './ReviewGlobalSettings';
import {
  ReviewRepositoryEditor,
  ReviewRepositoryList,
} from './ReviewRepositories';
import {
  emptyPolicyDraft,
  hasPolicy,
  invalidSettings,
} from './review-settings';
import type {
  PolicyDraft,
  SettingsDraft,
  SettingsIssue,
  SettingsPatch,
} from './review-settings';

export type ReviewSettingsDestination =
  'connections' | 'schedule' | 'repositories';

interface ReviewSettingsNavigation {
  section: ReviewSettingsDestination;
}

export function ReviewSettings({
  active,
  interactive,
  snapshot,
  values,
  dirty,
  stale,
  disabled,
  navigation,
  desktop,
  onEdit,
  onSave,
  onReload,
  onConnect,
  onDisconnect,
}: {
  active: boolean;
  interactive: boolean;
  snapshot: ReviewSnapshot;
  values: SettingsDraft;
  dirty: boolean;
  stale: boolean;
  disabled: boolean;
  navigation?: ReviewSettingsNavigation;
  desktop: AppWindowProps['desktop'];
  onEdit: (change: SettingsPatch) => void;
  onSave: () => void;
  onReload: () => void;
  onConnect: (provider: ReviewProvider) => void;
  onDisconnect: (provider: ReviewProvider) => void;
}) {
  const [selection, setSelection] = useState<{
    repository: string;
    navigation?: ReviewSettingsNavigation;
  }>();
  const [returnFocus, setReturnFocus] = useState('');
  const [issue, setIssue] = useState<SettingsIssue>();
  const form = useRef<HTMLFormElement>(null);
  const handledNavigation = useRef<ReviewSettingsNavigation | undefined>(
    undefined,
  );
  const localSelection = selection?.navigation === navigation;
  const selected = localSelection ? (selection?.repository ?? '') : '';
  const repository = values.repositories.find((item) => item === selected);
  const repoKey = repository?.toLowerCase() ?? '';
  const policy = values.policies[repoKey] ?? emptyPolicyDraft;
  const editingDisabled = disabled || stale;

  useEffect(() => {
    if (
      !active ||
      !interactive ||
      !navigation ||
      navigation === handledNavigation.current
    )
      return;
    const section = form.current?.querySelector<HTMLElement>(
      `[data-review-settings-section="${navigation.section}"]`,
    );
    if (!section) return;
    handledNavigation.current = navigation;
    section.focus({ preventScroll: true });
    section.scrollIntoView({ block: 'nearest' });
  }, [active, interactive, navigation]);

  function edit(change: SettingsPatch) {
    onEdit(change);
    setIssue(undefined);
  }

  function editPolicy(change: Partial<PolicyDraft>) {
    const next = { ...policy, ...change };
    const policies = { ...values.policies };
    if (hasPolicy(next)) policies[repoKey] = next;
    else delete policies[repoKey];
    edit({ policies });
  }

  function openRepository(name: string) {
    setIssue(undefined);
    setSelection({ repository: name, navigation });
  }

  function backToRepositories() {
    setReturnFocus(selected);
    setSelection({ repository: '', navigation });
    setIssue(undefined);
  }

  async function stopWatching() {
    if (!repository || editingDisabled) return;
    const answer = await desktop.dialog({
      title: `Stop watching ${repository}?`,
      message:
        'Saving will remove this repository and its rules and tests from the watch list. Existing reviews and receipts stay available. Direct review requests still appear.',
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        { label: 'Stop watching', value: 'stop' },
      ],
    });
    if (answer?.button !== 'stop') return;
    edit({
      repositories: values.repositories.filter((item) => item !== repository),
    });
    backToRepositories();
  }

  function prepareSave() {
    if (!dirty || editingDisabled) return;
    const invalid = invalidSettings(values);
    setIssue(invalid);
    if (invalid) {
      setSelection({
        repository: invalid.kind === 'repository' ? invalid.repository : '',
        navigation,
      });
      return;
    }
    onSave();
  }

  return (
    <div
      className="app-pad github-review-settings"
      hidden={!active}
      inert={!interactive || !active}
    >
      <form
        ref={form}
        className="github-review-settings-form"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          prepareSave();
        }}
      >
        {repository ? (
          <ReviewRepositoryEditor
            key={repository}
            repository={repository}
            policy={policy}
            environment={reviewEnvironment(snapshot, repository)}
            disabled={editingDisabled}
            onBack={backToRepositories}
            onChange={editPolicy}
            onStopWatching={() => void stopWatching()}
            issue={
              issue?.kind === 'repository' && repository === issue.repository
                ? issue.issue
                : undefined
            }
          />
        ) : (
          <>
            <div
              data-review-settings-section="connections"
              role="group"
              aria-label="Review connections"
              tabIndex={-1}
            >
              <ReviewConnections
                github={snapshot.github}
                codex={snapshot.codex}
                disabled={editingDisabled}
                onConnect={onConnect}
                onDisconnect={onDisconnect}
              />
            </div>
            <div
              data-review-settings-section="repositories"
              role="group"
              aria-label="Repository settings"
              tabIndex={-1}
            >
              <ReviewRepositoryList
                repositories={values.repositories}
                policies={values.policies}
                disabled={editingDisabled}
                onAdd={(names) =>
                  edit({ repositories: [...values.repositories, ...names] })
                }
                onConfigure={openRepository}
                returnFocus={(localSelection && returnFocus) || undefined}
              />
            </div>
            <div
              data-review-settings-section="schedule"
              role="group"
              aria-label="Scan settings"
              tabIndex={-1}
            >
              <ReviewScanSettings
                enabled={values.enabled}
                watchOnly={values.watchOnly}
                intervalMinutes={values.intervalMinutes}
                dailyLimit={values.dailyLimit}
                connectionsReady={
                  snapshot.github.connected && snapshot.codex.connected
                }
                savedEnabled={snapshot.config.enabled}
                savedDailyLimit={snapshot.config.dailyLimit}
                budget={snapshot.budget}
                disabled={editingDisabled}
                onChange={edit}
                issue={issue?.kind === 'schedule' ? issue.issue : undefined}
              />
            </div>
          </>
        )}
        {dirty && (
          <div className="github-review-save-settings">
            {stale ? (
              <>
                <span className="github-review-problem" role="alert">
                  Saved settings changed. Reload settings before editing again.
                </span>
                <Button disabled={disabled} onClick={onReload}>
                  Reload settings
                </Button>
              </>
            ) : (
              <>
                <span>Unsaved changes</span>
                <Button type="submit" primary disabled={disabled}>
                  Save settings
                </Button>
              </>
            )}
          </div>
        )}
      </form>
    </div>
  );
}
