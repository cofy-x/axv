import { useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { ComputeBinding, ImageImport, ImageInfo, Operation } from './api';
import { nameError, resourceBusy } from './form-state';
import { OperationFeedback, RequestDetails } from './OperationFeedback';
import { computeStore } from './compute-store';
import { OperationRequest, operationError } from './operation-requests';
import type { useCompute } from './useCompute';

type ImageInput = { binding: ComputeBinding } & (
  | { kind: 'import'; spec: ImageImport; file?: File }
  | { kind: 'delete'; image: ImageInfo }
);

function validImageURL(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function ImageManager({
  data,
  desktop,
  onShowActivity,
}: {
  data: ReturnType<typeof useCompute>;
  desktop: DesktopAPI;
  onShowActivity?: (id: string) => void;
}) {
  const [selectedId, setSelected] = useState('');
  const [selectedHost, setSelectedHost] = useState('');
  const [editing, setEditing] = useState(false);
  const [editingHost, setEditingHost] = useState('');
  const [editingArch, setEditingArch] = useState('');
  const [mode, setMode] = useState<'url' | 'file'>('url');
  const [name, setName] = useState('Linux development');
  const [url, setURL] = useState('');
  const [sha256, setSHA256] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState('');
  const [accepted, setAccepted] = useState<{
    operation: Operation;
    label: string;
  }>();
  const upload = useRef<AbortController | null>(null);
  const [requests] = useState(
    () =>
      new OperationRequest<
        ImageInput,
        { operation: Operation; image?: ImageInfo }
      >(async (input, id) => {
        if (input.kind === 'delete')
          return {
            operation: await machinesApi.deleteImage(
              input.image,
              id,
              input.binding,
            ),
          };
        if (input.file) {
          upload.current = new AbortController();
          setUploading(true);
          return machinesApi.importImage(
            input.file,
            input.spec,
            id,
            upload.current.signal,
            input.binding,
          );
        }
        return machinesApi.downloadImage(input.spec, id, input.binding);
      }),
  );
  const hostId = data.info?.hostId || '';
  const selected =
    selectedHost === hostId
      ? data.images.find((image) => image.id === selectedId)
      : undefined;
  const hostArch = data.info?.hostArch || data.info?.images?.[0]?.arch || '';
  const arch = editing ? editingArch : hostArch;
  const available = data.info?.providers?.some(
    (provider) => provider.available && provider.images,
  );
  const active = selected && resourceBusy(data.operations, selected.id);
  const usedBy = data.machines.filter(
    (machine) =>
      selectedHost === hostId &&
      machine.status !== 'deleted' &&
      machine.spec.imageId === selectedId,
  );
  const locked = busy || !!requests.pending;
  const canVersion =
    !locked && !editing && !data.error && !!available && !!selected;
  const canRemove =
    !locked &&
    !editing &&
    !data.error &&
    !!selected &&
    !active &&
    !usedBy.length;
  const validation =
    (editing && editingHost !== hostId
      ? 'This draft belongs to another VM host. Cancel it and open a new form for the current host.'
      : '') ||
    nameError(name) ||
    (!arch
      ? 'The host architecture is not available yet.'
      : !/^[a-fA-F0-9]{64}$/.test(sha256)
        ? 'Enter the 64-character SHA-256 supplied by the image publisher.'
        : mode === 'url' && !validImageURL(url)
          ? 'Enter an HTTPS image URL without embedded credentials.'
          : mode === 'file' &&
              (!file || !file.size || file.size > 8 * 1024 ** 3)
            ? 'Select a non-empty Linux cloud image file no larger than 8 GiB.'
            : '');

  function begin(version = false) {
    setName(
      version ? `${selected?.name || 'Linux'} update` : 'Linux development',
    );
    setURL('');
    setSHA256('');
    setFile(null);
    setMode('url');
    setError('');
    setEditingHost(hostId);
    setEditingArch(hostArch);
    setEditing(true);
  }

  async function run(input?: ImageInput, label?: string) {
    const originalInput = input || requests.pending?.input;
    const latest = computeStore.getSnapshot();
    if (
      !latest.info?.hostId ||
      originalInput?.binding.hostId !== latest.info.hostId
    ) {
      setError(
        'This request belongs to another VM host. Reconnect to its original host to resolve it.',
      );
      return;
    }
    if (latest.error) {
      setError('Reconnect to the host before making changes.');
      return;
    }
    setError('');
    setBusy(true);
    const requestLabel = label || requests.pending?.label || 'Image operation';
    try {
      const result = input
        ? await requests.submit(input, requestLabel)
        : await requests.retry();
      if (result.image) {
        setSelected(result.image.id);
        setSelectedHost(originalInput.binding.hostId || '');
      }
      setEditing(false);
      setAccepted({ operation: result.operation, label: requestLabel });
      data.refresh();
    } catch (failure) {
      setError(operationError(failure));
    } finally {
      upload.current = null;
      setUploading(false);
      setBusy(false);
    }
  }

  function submit() {
    if (validation || data.error || !available) return;
    void run(
      {
        kind: 'import',
        binding: { hostId: editingHost },
        spec: {
          name: name.trim(),
          arch,
          sha256: sha256.toLowerCase(),
          ...(mode === 'url' ? { url: url.trim() } : {}),
        },
        ...(mode === 'file' && file ? { file } : {}),
      },
      `Import “${name.trim()}”`,
    );
  }

  async function remove() {
    if (!selected) return;
    const answer = await desktop.dialog({
      title: 'Remove image?',
      message: selected.managed
        ? `Remove “${selected.name}” and its managed files? Images used by VMs, backups or templates cannot be removed.`
        : `Remove “${selected.name}” from the image catalog? Its administrator-supplied source files remain on disk.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        { label: 'Remove Image', value: 'remove' },
      ],
    });
    if (answer?.button === 'remove')
      await run(
        { kind: 'delete', image: selected, binding: { hostId } },
        `Remove “${selected.name}”`,
      );
  }

  const current = useRef({
    hostId,
    selected,
    canVersion,
    canRemove,
    begin,
    remove,
  });
  current.current = { hostId, selected, canVersion, canRemove, begin, remove };

  return (
    <div>
      <div className="app-toolbar machines-toolbar">
        <Button
          disabled={locked || editing || !!data.error || !available}
          onClick={() => begin()}
        >
          Add Image
        </Button>
        <div className="machines-resource-actions">
          <IconButton
            icon="more"
            label="More image actions"
            aria-haspopup="menu"
            disabled={!selected}
            onClick={(event) => {
              if (!selected) return;
              const targetHost = hostId;
              const targetId = selected.id;
              const rect = event.currentTarget.getBoundingClientRect();
              const invoke = (action: 'version' | 'remove') => {
                const latest = current.current;
                if (
                  latest.hostId !== targetHost ||
                  latest.selected?.id !== targetId ||
                  !(action === 'version' ? latest.canVersion : latest.canRemove)
                )
                  return;
                if (action === 'version') latest.begin(true);
                else void latest.remove();
              };
              desktop.contextMenu(
                [
                  {
                    label: 'Add Version',
                    disabled: !canVersion,
                    action: () => invoke('version'),
                  },
                  { separator: true },
                  {
                    label: 'Remove',
                    disabled: !canRemove,
                    action: () => invoke('remove'),
                  },
                ],
                { x: rect.left, y: rect.bottom },
                { title: selected.name || selected.id },
              );
            }}
          />
        </div>
      </div>
      {error && (
        <p className="machines-error" role="alert">
          {error}
        </p>
      )}
      <OperationFeedback
        accepted={accepted}
        operations={data.operations}
        onShowActivity={onShowActivity}
      />
      {requests.pending && !busy && (
        <div className="machines-operation-feedback">
          <p>
            The response to {requests.pending.label} was interrupted. Retry the
            original request to check whether it was accepted. A cancelled
            upload may already have reached the host.
          </p>
          {requests.pending.input.binding.hostId !== hostId && (
            <p>Reconnect to the original VM host to resolve this request.</p>
          )}
          <Button
            disabled={
              !!data.error || requests.pending.input.binding.hostId !== hostId
            }
            onClick={() => void run()}
          >
            Retry Original Request
          </Button>
          <RequestDetails
            requestId={requests.pending.requestId}
            hostId={requests.pending.input.binding.hostId}
          />
        </div>
      )}
      {!available && (
        <p className="machines-form-help">
          {data.info
            ? 'An available provider with image import support is required to add images.'
            : 'Checking host image support…'}
        </p>
      )}
      {selectedId && !selected && (
        <p className="machines-warning">
          The selected image is no longer present. Select another image
          explicitly.
        </p>
      )}
      {!!usedBy.length && (
        <p className="machines-form-help">
          Used by {usedBy.map((machine) => machine.spec.name).join(', ')}.
          Delete those VMs before removing this image.
        </p>
      )}
      {selected?.status === 'failed' && (
        <p className="machines-warning">
          Remove this failed import, then add a new image with a verified source
          and checksum.
        </p>
      )}
      {editing && (
        <form
          className="app-pad machines-form"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <fieldset disabled={locked}>
            <legend>New image version · {arch}</legend>
            <Field
              label="Image name"
              value={name}
              maxLength={80}
              required
              onChange={(event) => setName(event.target.value)}
            />
            <Select
              label="Source"
              value={mode}
              onChange={(event) => {
                setMode(event.target.value as 'url' | 'file');
                setFile(null);
              }}
            >
              <option value="url">HTTPS download</option>
              <option value="file">Local image file</option>
            </Select>
            {mode === 'url' ? (
              <Field
                label="Image URL"
                type="url"
                value={url}
                required
                onChange={(event) => setURL(event.target.value)}
              />
            ) : (
              <Field
                label="Linux cloud image"
                type="file"
                accept=".img,.qcow2,.raw"
                required
                onChange={(event) => setFile(event.target.files?.[0] || null)}
              />
            )}
            <Field
              label="Expected SHA-256"
              value={sha256}
              minLength={64}
              maxLength={64}
              pattern="[a-fA-F0-9]{64}"
              required
              onChange={(event) => setSHA256(event.target.value.trim())}
            />
            <p className="machines-form-help">
              Use a native {arch} Linux cloud image with cloud-init, systemd and
              apt. Files are limited to 8 GiB. Compare the checksum with the
              publisher’s value. Existing VMs keep their current image version.
            </p>
            {validation && <p className="machines-warning">{validation}</p>}
            <div className="machines-form-actions">
              <Button
                type="submit"
                disabled={!!validation || !!data.error || !available}
              >
                {mode === 'file' ? 'Upload and Verify' : 'Download and Verify'}
              </Button>
              <Button onClick={() => setEditing(false)}>Cancel</Button>
            </div>
          </fieldset>
          {busy && (
            <p role="status">
              {uploading ? 'Uploading image…' : 'Submitting request…'}{' '}
              {uploading && (
                <Button onClick={() => upload.current?.abort()}>
                  Cancel Upload
                </Button>
              )}
            </p>
          )}
        </form>
      )}
      <table className="machines-table">
        <thead>
          <tr>
            <th>Image</th>
            <th>Compatibility</th>
            <th>State</th>
          </tr>
        </thead>
        <tbody>
          {data.images.map((image) => (
            <tr key={image.id} data-selected={selected?.id === image.id}>
              <td>
                <button
                  className="machines-select"
                  aria-pressed={selected?.id === image.id}
                  onClick={() => {
                    setSelected(image.id);
                    setSelectedHost(hostId);
                  }}
                >
                  {image.name}
                </button>
                <details>
                  <summary>Details</summary>
                  <code>{image.id}</code>
                  {image.sha256 && (
                    <small>
                      SHA-256 <code>{image.sha256}</code>
                    </small>
                  )}
                </details>
              </td>
              <td>
                {image.arch} · {image.provider}
                <small>
                  {image.minRootGiB
                    ? `Root disk ≥ ${image.minRootGiB} GiB`
                    : 'Configured cloud image'}
                </small>
                {!!image.sizeBytes && (
                  <small>{(image.sizeBytes / 1024 ** 2).toFixed(0)} MiB</small>
                )}
              </td>
              <td>
                {image.status || 'configured'}
                <small>
                  {image.source ||
                    (image.managed ? 'Managed' : 'Host configuration')}
                </small>
                {image.reason && (
                  <small className="machines-failure">{image.reason}</small>
                )}
              </td>
            </tr>
          ))}
          {!data.images.length && (
            <tr>
              <td colSpan={3}>
                No registered images. Add a verified Linux cloud image.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
