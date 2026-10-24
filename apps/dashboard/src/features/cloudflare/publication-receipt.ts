import { sameProjectBinding } from '../project-files/vm-init-api';
import type { CloudflarePublication, CloudflarePublishRequest } from './types';

export function publicationName(value: string) {
  return (
    !value.startsWith('xn--') &&
    /^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$/.test(value)
  );
}

export function allocatedPublicationHostname(
  record: Pick<
    CloudflarePublication,
    'name' | 'generation' | 'domain' | 'hostname'
  >,
) {
  if (
    typeof record.name !== 'string' ||
    !publicationName(record.name) ||
    typeof record.generation !== 'string' ||
    !/^[a-f0-9]{32}$/.test(record.generation) ||
    typeof record.domain !== 'string' ||
    !record.domain.includes('.') ||
    !record.domain
      .split('.')
      .every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
  )
    return undefined;
  const hostname = `${record.name}-${record.generation}.${record.domain}`;
  return hostname.length <= 253 && record.hostname === hostname
    ? hostname
    : undefined;
}

export function checkPublicationReceipt(
  record: CloudflarePublication,
  submitted: CloudflarePublishRequest,
  reviewedDomain: string,
) {
  if (
    !record?.id ||
    record.requestId !== submitted.requestId ||
    record.name !== submitted.name ||
    !reviewedDomain ||
    submitted.domain !== reviewedDomain ||
    record.domain !== reviewedDomain ||
    !allocatedPublicationHostname(record) ||
    record.visibility !== submitted.visibility ||
    record.port !== submitted.port ||
    !record.binding ||
    !sameProjectBinding(record.binding, submitted.binding)
  )
    throw new Error(
      'AXV returned a different publication receipt. Your original draft and request ID are kept. Refresh and inspect the original publication before another request.',
    );
  return record;
}

export function checkPublicationActionReceipt(
  receipt: CloudflarePublication,
  original: CloudflarePublication,
) {
  checkPublicationReceipt(
    receipt,
    {
      instanceId: '',
      requestId: original.requestId,
      name: original.name,
      domain: original.domain,
      binding: original.binding,
      port: original.port,
      visibility: original.visibility,
    },
    original.domain,
  );
  if (
    receipt.id !== original.id ||
    receipt.generation !== original.generation ||
    receipt.hostname !== original.hostname
  )
    throw new Error(
      'AXV returned a different publication receipt. The original publication is kept. Refresh and inspect it before another operation.',
    );
  return receipt;
}
