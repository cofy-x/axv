import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';

const GitHubReview = deferWindow(() =>
  import('./GitHubReview').then((module) => module.GitHubReview),
);

export const githubReviewApp: AppDefinition = {
  id: 'github-review',
  title: 'GitHub Review',
  icon: 'code-review',
  launchable: true,
  defaultPinned: false,
  defaultDesktop: false,
  windowKey: () => 'main',
  component: GitHubReview,
  defaultSize: { width: 720, height: 690 },
  minSize: { width: 320, height: 300 },
};
