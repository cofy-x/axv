import type { AppDefinition } from '@cofy-x/axv-nextstep';
import { DocumentWorkflow } from './DocumentWorkflow';
import { ConversationContext } from './ConversationContext';
import { SnapshotPreview } from './SnapshotPreview';
import './document-workflow.css';

export const snapshotPreviewApp: AppDefinition = {
  id: 'document-snapshot',
  title: 'Document Snapshot',
  icon: 'document',
  launchable: false,
  component: SnapshotPreview,
  windowKey: (params) => params.snapshotId || 'unavailable',
  defaultSize: { width: 560, height: 480 },
  minSize: { width: 300, height: 260 },
};

export const documentWorkflowApp: AppDefinition = {
  id: 'document-workflow',
  title: 'Document actions',
  icon: 'document',
  kind: 'panel',
  launchable: false,
  component: DocumentWorkflow,
  windowKey: (params) => params.entryId || 'unavailable',
  defaultSize: { width: 510, height: 570 },
  minSize: { width: 300, height: 300 },
};

export const conversationContextApp: AppDefinition = {
  id: 'conversation-context',
  title: 'Attachments',
  icon: 'document',
  kind: 'panel',
  launchable: false,
  component: ConversationContext,
  windowKey: (params) => `${params.sessionId}:${params.runId}`,
  defaultSize: { width: 520, height: 520 },
  minSize: { width: 300, height: 300 },
};
