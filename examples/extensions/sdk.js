// AXV extension API 1. Inline this file into a self-contained HTML package.
// The transferred MessagePort is the only channel to the host.
(() => {
  let port;
  let initialized = false;
  let sequence = 0;
  let resolveReady;
  const pending = new Map();
  const listeners = new Set();
  const ready = new Promise((resolve) => {
    resolveReady = resolve;
  });

  function emit(message) {
    for (const listener of listeners) listener(message);
  }

  function request(method, params) {
    if (!port || !initialized)
      return Promise.reject(new Error('The connection to AXV is not ready.'));
    const id = String(++sequence);
    return new Promise((resolve, reject) => {
      const timer = globalThis.setTimeout(() => {
        pending.delete(id);
        reject(new Error('AXV did not answer. Your changes are still here.'));
      }, 15000);
      pending.set(id, { resolve, reject, timer });
      port.postMessage({ type: 'request', id, method, params });
    });
  }

  function receive(event) {
    const message = event.data;
    if (!message || typeof message.type !== 'string') return;
    if (message.type === 'init' && !initialized) {
      initialized = true;
      resolveReady(message);
    } else if (message.type === 'response' && typeof message.id === 'string') {
      const call = pending.get(message.id);
      if (!call) return;
      pending.delete(message.id);
      globalThis.clearTimeout(call.timer);
      if (message.error) {
        const error = new Error(message.error.message || 'The request failed.');
        error.code = message.error.code;
        call.reject(error);
      } else call.resolve(message.result);
    } else if (
      initialized &&
      ['lifecycle', 'saved', 'discarded'].includes(message.type)
    ) {
      emit(message);
    }
  }

  globalThis.addEventListener('message', (event) => {
    if (
      port ||
      event.source !== globalThis.parent ||
      !event.data ||
      event.data.axv !== 'connect' ||
      event.data.version !== 1 ||
      typeof event.data.nonce !== 'string' ||
      !event.data.nonce ||
      event.ports.length !== 1
    )
      return;
    port = event.ports[0];
    port.onmessage = receive;
    port.start();
    port.postMessage({ type: 'ready', nonce: event.data.nonce });
  });

  globalThis.axv = Object.freeze({
    ready,
    request,
    onEvent(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
})();
