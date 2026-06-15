const BASE = '';  // proxied to :8080 in dev; set REACT_APP_API_URL in prod

export const api = {
  async getStats() {
    const r = await fetch(`${BASE}/v1/stats`);
    if (!r.ok) throw new Error('stats failed');
    return r.json();
  },

  async getKey(key) {
    const r = await fetch(`${BASE}/v1/keys/${encodeURIComponent(key)}`);
    if (r.status === 404) return null;
    if (!r.ok) throw new Error(await r.text());
    const data = await r.json();
    // value is base64 — decode to string
    try { return atob(data.value); } catch { return data.value; }
  },

  async putKey(key, value) {
    const r = await fetch(`${BASE}/v1/keys/${encodeURIComponent(key)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ value }),
    });
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  },

  async deleteKey(key) {
    const r = await fetch(`${BASE}/v1/keys/${encodeURIComponent(key)}`, { method: 'DELETE' });
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  },

  async batch(ops) {
    const r = await fetch(`${BASE}/v1/batch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ops }),
    });
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  },

  async health() {
    try {
      const r = await fetch(`${BASE}/healthz`);
      return r.ok;
    } catch { return false; }
  },
};
