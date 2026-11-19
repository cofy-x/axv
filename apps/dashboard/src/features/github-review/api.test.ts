import { describe, expect, it } from 'vitest';
import {
  reviewCheckURL,
  reviewCommentURL,
  reviewLoginURL,
  reviewPRURL,
} from './api';
import type { ReviewJob } from './api';

describe('GitHub Review links', () => {
  it('links only to the expected device authorization page for each provider', () => {
    const github = 'https://github.com/login/device';
    const codex = 'https://auth.openai.com/codex/device';
    expect(reviewLoginURL('github', github)).toBe(github);
    expect(reviewLoginURL('codex', codex)).toBe(codex);
    expect(reviewLoginURL('github', codex)).toBeUndefined();
    expect(
      reviewLoginURL('github', 'https://github.com.evil.test/login/device'),
    ).toBeUndefined();
    expect(reviewLoginURL('codex', `${codex}?redirect=other`)).toBeUndefined();
    expect(reviewLoginURL('github', 'javascript:alert(1)')).toBeUndefined();
  });

  it('opens check runs on GitHub and keeps other check providers as text', () => {
    expect(reviewCheckURL('https://github.com/o/r/actions/runs/42')).toBe(
      'https://github.com/o/r/actions/runs/42',
    );
    expect(reviewCheckURL('https://github.com.test/o/r/check')).toBeUndefined();
    expect(
      reviewCheckURL('https://token@github.com/o/r/check'),
    ).toBeUndefined();
    expect(reviewCheckURL('javascript:alert(1)')).toBeUndefined();
  });

  it('binds comment links to the original repository and pull request', () => {
    const job: ReviewJob = {
      repository: 'Example/project',
      number: 42,
      baseSha: '101ff91',
      headSha: '45c14e3',
      source: 'requested',
      status: 'published',
      updatedAt: '2026-11-19T01:30:00Z',
    };
    const prURL = 'https://github.com/Example/project/pull/42';
    const commentURL = `${prURL}#issuecomment-24`;
    expect(reviewPRURL(job.repository, job.number)).toBe(prURL);
    expect(reviewPRURL('Example/..', job.number)).toBeUndefined();
    expect(reviewCommentURL({ ...job, commentUrl: commentURL })).toBe(
      commentURL,
    );
    expect(
      reviewCommentURL({ ...job, number: 43, commentUrl: commentURL }),
    ).toBeUndefined();
    expect(
      reviewCommentURL({ ...job, commentUrl: `${prURL}#issuecomment` }),
    ).toBeUndefined();
  });
});
