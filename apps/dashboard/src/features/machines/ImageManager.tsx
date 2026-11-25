import { useRef, useState } from 'react';
import { Button, Field, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { ComputeBinding, ImageImport, ImageInfo, Operation } from './api';
import { nameError, resourceBusy } from './form-state';
import { OperationFeedback, RequestDetails } from './OperationFeedback';
import { computeStore } from './compute-store';
import { OperationRequest, operationError } from './operation-requests';
import { humanize, linuxVMSetupGuide } from './presentation';
import type { useCompute } from './useCompute';
import { HostCommand } from './HostServiceGuidance';

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
  defaultSetupCommand,
}: {
  data: ReturnType<typeof useCompute>;
  desktop: DesktopAPI;
  onShowActivity?: (id: string) => void;
  defaultSetupCommand?: string;
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
  const providers = data.info?.providers || [];
  const available = providers.some(
    (provider) => provider.available && provider.images,
  );
  const administratorImages =
    providers.some((provider) => provider.provider === 'firecracker') &&
    !providers.some((provider) => provider.images);
  const defaultImageId = data.preparation.check?.defaultImageId;
  const showImageMenu = !!selected && selected.id !== defaultImageId;
  const imageUsers = (image: ImageInfo) =>
    data.machines.filter(
      (machine) =>
        machine.status !== 'deleted' && machine.spec.imageId === image.id,
    );
  const usedBy = selected ? imageUsers(selected) : [];
  const locked = busy || !!requests.pending;
  const canVersion =
    !locked && !editing && !data.error && !!available && !!selected;
  const canRemoveImage = (image: ImageInfo, hasUsers: boolean) =>
    !locked &&
    !editing &&
    !data.error &&
    !resourceBusy(data.operations, image.id) &&
    image.id !== defaultImageId &&
    !hasUsers;
  const canRemove = !!selected && canRemoveImage(selected, !!usedBy.length);
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

  async function remove(image: ImageInfo) {
    const answer = await desktop.dialog({
      title: 'Remove image?',
      message: image.managed
        ? `Remove “${image.name}” and its managed files? Images used by VMs, backups or templates cannot be removed.`
        : `Remove “${image.name}” from the image catalog? Its administrator-supplied source files remain on disk.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        { label: 'Remove Image', value: 'remove' },
      ],
    });
    if (answer?.button !== 'remove') return;
    const latest = computeStore.getSnapshot();
    if (
      latest.info?.hostId === hostId &&
      data.prepare.getSnapshot().check?.defaultImageId === image.id
    ) {
      setError('The default AXV Guest image is managed by the VM host.');
      return;
    }
    await run(
      { kind: 'delete', image, binding: { hostId } },
      `Remove “${image.name}”`,
    );
  }

  async function showDetails(image: ImageInfo) {
    const imageMachines = imageUsers(image);
    const answer = await desktop.dialog({
      title: 'Image Details',
      showIcon: false,
      message: (
        <div className="machines-resource-details">
          <strong>{image.name}</strong>
          <dl className="info-list">
            <dt>Source</dt>
            <dd>
              {image.source ||
                (image.managed ? 'Managed' : 'Host configuration')}
            </dd>
            {!!image.minRootGiB && (
              <>
                <dt>Minimum root disk</dt>
                <dd>{image.minRootGiB} GiB</dd>
              </>
            )}
            {!!image.sizeBytes && (
              <>
                <dt>Image size</dt>
                <dd>{(image.sizeBytes / 1024 ** 2).toFixed(0)} MiB</dd>
              </>
            )}
            <dt>Image ID</dt>
            <dd>
              <code>{image.id}</code>
            </dd>
            {image.sha256 && (
              <>
                <dt>SHA-256</dt>
                <dd>
                  <code>{image.sha256}</code>
                </dd>
              </>
            )}
          </dl>
          {administratorImages && !!imageMachines.length && (
            <p>
              Used by{' '}
              {imageMachines.map((machine) => machine.spec.name).join(', ')}.
            </p>
          )}
        </div>
      ),
      buttons: [
        ...(administratorImages && canRemoveImage(image, !!imageMachines.length)
          ? [{ label: 'Remove', value: 'remove' }]
          : []),
        { label: 'Close', value: 'close', default: true, cancel: true },
      ],
    });
    if (answer?.button === 'remove') await remove(image);
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

  if (
    defaultSetupCommand &&
    !editing &&
    !requests.pending &&
    !data.images.length
  ) {
    if (data.reloading) return null;
    return (
      <section className="machines-environment">
        <header className="machines-section-heading">
          <h2>Install the default VM image</h2>
        </header>
        <div className="machines-host-guidance">
          <p>
            Install the AXV Guest and kernel (about 135 MiB). The catalog
            refreshes after the service restarts.
          </p>
          <HostCommand
            key={defaultSetupCommand}
            command={defaultSetupCommand}
            desktop={desktop}
          />
        </div>
      </section>
    );
  }

  return (
    <div>
      {!administratorImages && (
        <div className="app-toolbar machines-toolbar">
          <Button
            disabled={locked || editing || !!data.error || !available}
            onClick={() => begin()}
          >
            Add Image
          </Button>
          {showImageMenu && (
            <div className="machines-resource-actions">
              <IconButton
                icon="more"
                label="More image actions"
                aria-haspopup="menu"
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
                      !(action === 'version'
                        ? latest.canVersion
                        : latest.canRemove)
                    )
                      return;
                    if (action === 'version') latest.begin(true);
                    else void latest.remove(latest.selected);
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
          )}
        </div>
      )}
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
      {administratorImages && !data.images.length ? (
        <p className="machines-message">
          The deployment host administrator registers Firecracker guest images
          in the VM host configuration. See the{' '}
          <a href={linuxVMSetupGuide} target="_blank" rel="noreferrer">
            Linux VM setup guide
          </a>
          .
        </p>
      ) : (
        !administratorImages &&
        !available && (
          <p className="machines-form-help">
            {data.info
              ? 'An available provider with image import support is required to add images.'
              : 'Checking host image support…'}
          </p>
        )
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
      <table className="machines-table machines-inventory-table">
        <colgroup>
          <col />
          <col className="machines-inventory-secondary-column" />
          <col className="machines-details-column" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">Image</th>
            <th scope="col">Status</th>
            <th scope="col" aria-label="Details" />
          </tr>
        </thead>
        <tbody>
          {data.images.map((image) => (
            <tr key={image.id} data-selected={selected?.id === image.id}>
              <td>
                {administratorImages || image.id === defaultImageId ? (
                  <strong>{image.name}</strong>
                ) : (
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
                )}
                <small>
                  {humanize(image.provider)} · {image.arch}
                </small>
              </td>
              <td>
                {!image.status ||
                image.status === 'configured' ||
                image.status === 'ready'
                  ? providers.some(
                      (provider) =>
                        provider.provider === image.provider &&
                        provider.available,
                    ) &&
                    (!hostArch || image.arch === hostArch)
                    ? 'Ready'
                    : 'Registered'
                  : humanize(image.status)}
                {image.reason && (
                  <small className="machines-failure">{image.reason}</small>
                )}
              </td>
              <td className="machines-inventory-action">
                <Button
                  className="machines-text-button"
                  aria-label={`Image details for ${image.name}`}
                  aria-haspopup="dialog"
                  onClick={() => void showDetails(image)}
                >
                  Details
                </Button>
              </td>
            </tr>
          ))}
          {!data.images.length && (
            <tr>
              <td colSpan={3}>
                {administratorImages
                  ? 'No Firecracker guest images are registered.'
                  : 'No registered images. Add a verified Linux cloud image.'}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
