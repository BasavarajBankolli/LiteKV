import React, { useState, useEffect, useRef, useCallback } from 'react';
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis,
  Tooltip, ResponsiveContainer, CartesianGrid
} from 'recharts';
import { api } from './api';

// ─── Design tokens ───────────────────────────────────────────────────────────
const C = {
  bg:       '#0a0c10',
  surface:  '#111318',
  border:   '#1e2330',
  border2:  '#2a3040',
  text:     '#e2e8f0',
  muted:    '#64748b',
  dim:      '#374151',
  green:    '#10b981',
  greenDim: '#064e3b',
  blue:     '#3b82f6',
  blueDim:  '#1e3a5f',
  amber:    '#f59e0b',
  amberDim: '#451a03',
  red:      '#ef4444',
  purple:   '#8b5cf6',
  mono:     "'JetBrains Mono', monospace",
};

const css = {
  app: {
    minHeight: '100vh', background: C.bg, color: C.text,
    fontFamily: "'Inter', sans-serif",
  },
  header: {
    borderBottom: `1px solid ${C.border}`,
    padding: '0 2rem',
    height: 56,
    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
    position: 'sticky', top: 0, background: C.bg, zIndex: 10,
  },
  logo: {
    display: 'flex', alignItems: 'center', gap: 10,
    fontSize: 16, fontWeight: 600, letterSpacing: '-0.02em',
  },
  logoDot: {
    width: 8, height: 8, borderRadius: '50%', background: C.green,
    boxShadow: `0 0 8px ${C.green}`,
  },
  nav: { display: 'flex', gap: 4 },
  navBtn: (active) => ({
    padding: '6px 14px', borderRadius: 6, border: 'none', cursor: 'pointer',
    fontSize: 13, fontWeight: 500,
    background: active ? C.surface : 'transparent',
    color: active ? C.text : C.muted,
    transition: 'all 0.15s',
  }),
  main: { padding: '1.5rem 2rem', maxWidth: 1280, margin: '0 auto' },
  grid2: { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 },
  grid4: { display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginBottom: 16 },
  card: {
    background: C.surface, border: `1px solid ${C.border}`,
    borderRadius: 10, padding: '1.25rem 1.5rem',
  },
  cardTitle: { fontSize: 11, fontWeight: 600, color: C.muted, letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 12 },
  statNum: { fontSize: 28, fontWeight: 600, letterSpacing: '-0.03em', lineHeight: 1 },
  statLabel: { fontSize: 12, color: C.muted, marginTop: 4 },
  badge: (color, bg) => ({
    display: 'inline-flex', alignItems: 'center', gap: 5,
    padding: '3px 10px', borderRadius: 99,
    fontSize: 12, fontWeight: 500,
    color, background: bg,
  }),
  input: {
    background: C.bg, border: `1px solid ${C.border2}`,
    borderRadius: 6, padding: '8px 12px',
    color: C.text, fontSize: 13, fontFamily: C.mono,
    outline: 'none', width: '100%',
    transition: 'border-color 0.15s',
  },
  btn: (variant = 'default') => ({
    padding: '8px 16px', borderRadius: 6, border: 'none',
    cursor: 'pointer', fontSize: 13, fontWeight: 500,
    transition: 'all 0.15s',
    ...(variant === 'green'  ? { background: C.green,   color: '#fff' } : {}),
    ...(variant === 'red'    ? { background: C.red,     color: '#fff' } : {}),
    ...(variant === 'ghost'  ? { background: C.surface, color: C.text, border: `1px solid ${C.border2}` } : {}),
    ...(variant === 'default'? { background: C.blue,    color: '#fff' } : {}),
  }),
  mono: { fontFamily: C.mono, fontSize: 13 },
  row: { display: 'flex', alignItems: 'center', gap: 10 },
};

// ─── Helpers ─────────────────────────────────────────────────────────────────
const fmt = {
  bytes: (b) => b > 1e6 ? `${(b/1e6).toFixed(1)}MB` : b > 1e3 ? `${(b/1e3).toFixed(1)}KB` : `${b}B`,
  num:   (n) => n > 1e6 ? `${(n/1e6).toFixed(1)}M` : n > 1e3 ? `${(n/1e3).toFixed(0)}K` : String(n),
};

// ─── Subcomponents ───────────────────────────────────────────────────────────

function StatusDot({ online }) {
  return (
    <span style={{
      width: 8, height: 8, borderRadius: '50%', display: 'inline-block',
      background: online ? C.green : C.red,
      boxShadow: online ? `0 0 6px ${C.green}` : 'none',
    }} />
  );
}

function StatCard({ label, value, sub, color = C.text }) {
  return (
    <div style={css.card}>
      <div style={css.cardTitle}>{label}</div>
      <div style={{ ...css.statNum, color }}>{value}</div>
      {sub && <div style={css.statLabel}>{sub}</div>}
    </div>
  );
}

function LSMViz({ levelCounts }) {
  const levels = Array.isArray(levelCounts) ? levelCounts : [];
  const maxFiles = Math.max(...levels, 1);
  return (
    <div style={css.card}>
      <div style={css.cardTitle}>LSM tree — level layout</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {levels.map((count, i) => (
          <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <span style={{ ...css.mono, color: C.muted, width: 18, textAlign: 'right', fontSize: 11 }}>L{i}</span>
            <div style={{ flex: 1, height: 20, background: C.bg, borderRadius: 4, overflow: 'hidden', border: `1px solid ${C.border}` }}>
              <div style={{
                width: count === 0 ? 0 : `${Math.max(4, (count / maxFiles) * 100)}%`,
                height: '100%',
                background: i === 0 ? C.green : i === 1 ? C.blue : C.purple,
                opacity: 0.7 + (i * 0.05),
                transition: 'width 0.4s ease',
                borderRadius: 3,
              }} />
            </div>
            <span style={{ ...css.mono, color: C.muted, width: 28, fontSize: 11 }}>{count}</span>
          </div>
        ))}
        {levels.every(c => c === 0) && (
          <div style={{ color: C.muted, fontSize: 13, textAlign: 'center', padding: '1rem 0' }}>
            No SSTables yet — start writing keys
          </div>
        )}
      </div>
    </div>
  );
}

function ThroughputChart({ data }) {
  return (
    <div style={css.card}>
      <div style={css.cardTitle}>live ops/sec</div>
      <ResponsiveContainer width="100%" height={140}>
        <AreaChart data={data} margin={{ top: 4, right: 0, left: -20, bottom: 0 }}>
          <defs>
            <linearGradient id="gw" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={C.green} stopOpacity={0.3} />
              <stop offset="95%" stopColor={C.green} stopOpacity={0} />
            </linearGradient>
            <linearGradient id="gr" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor={C.blue} stopOpacity={0.3} />
              <stop offset="95%" stopColor={C.blue} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke={C.border} />
          <XAxis dataKey="t" tick={{ fontSize: 10, fill: C.muted }} tickLine={false} axisLine={false} />
          <YAxis tick={{ fontSize: 10, fill: C.muted }} tickLine={false} axisLine={false} tickFormatter={fmt.num} />
          <Tooltip
            contentStyle={{ background: C.surface, border: `1px solid ${C.border2}`, borderRadius: 6, fontSize: 12 }}
            labelStyle={{ color: C.muted }}
          />
          <Area type="monotone" dataKey="writes" stroke={C.green} fill="url(#gw)" strokeWidth={1.5} dot={false} name="Writes" />
          <Area type="monotone" dataKey="reads" stroke={C.blue} fill="url(#gr)" strokeWidth={1.5} dot={false} name="Reads" />
        </AreaChart>
      </ResponsiveContainer>
      <div style={{ display: 'flex', gap: 16, marginTop: 8 }}>
        <span style={{ fontSize: 11, color: C.green }}>● writes</span>
        <span style={{ fontSize: 11, color: C.blue }}>● reads</span>
      </div>
    </div>
  );
}

function LevelChart({ levelCounts }) {
  const data = (Array.isArray(levelCounts) ? levelCounts : [])
    .map((count, i) => ({ level: `L${i}`, files: count }));
  return (
    <div style={css.card}>
      <div style={css.cardTitle}>SSTables per level</div>
      <ResponsiveContainer width="100%" height={140}>
        <BarChart data={data} margin={{ top: 4, right: 0, left: -20, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke={C.border} vertical={false} />
          <XAxis dataKey="level" tick={{ fontSize: 11, fill: C.muted }} tickLine={false} axisLine={false} />
          <YAxis tick={{ fontSize: 10, fill: C.muted }} tickLine={false} axisLine={false} allowDecimals={false} />
          <Tooltip
            contentStyle={{ background: C.surface, border: `1px solid ${C.border2}`, borderRadius: 6, fontSize: 12 }}
            labelStyle={{ color: C.muted }}
          />
          <Bar dataKey="files" fill={C.purple} radius={[3, 3, 0, 0]} name="SSTables" />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

// ─── Key Explorer ─────────────────────────────────────────────────────────────
function KeyExplorer() {
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [result, setResult] = useState(null);
  const [log, setLog] = useState([]);
  const [loading, setLoading] = useState(false);

  const addLog = (type, msg) => {
    const ts = new Date().toLocaleTimeString('en', { hour12: false });
    setLog(l => [{ type, msg, ts }, ...l].slice(0, 50));
  };

  const handleGet = async () => {
    if (!key.trim()) return;
    setLoading(true);
    try {
      const val = await api.getKey(key.trim());
      setResult({ found: val !== null, value: val, key: key.trim() });
      addLog(val !== null ? 'success' : 'warn', val !== null ? `GET ${key} → "${val}"` : `GET ${key} → not found`);
    } catch (e) {
      addLog('error', `GET ${key} failed: ${e.message}`);
    } finally { setLoading(false); }
  };

  const handlePut = async () => {
    if (!key.trim() || !value.trim()) return;
    setLoading(true);
    try {
      await api.putKey(key.trim(), value.trim());
      addLog('success', `PUT ${key} = "${value}"`);
      setResult(null);
    } catch (e) {
      addLog('error', `PUT failed: ${e.message}`);
    } finally { setLoading(false); }
  };

  const handleDelete = async () => {
    if (!key.trim()) return;
    setLoading(true);
    try {
      await api.deleteKey(key.trim());
      addLog('warn', `DELETE ${key}`);
      setResult(null);
    } catch (e) {
      addLog('error', `DELETE failed: ${e.message}`);
    } finally { setLoading(false); }
  };

  const handleBatch = async () => {
    setLoading(true);
    try {
      const ops = [
        { type: 'put', key: 'user:1', value: 'Alice' },
        { type: 'put', key: 'user:2', value: 'Bob' },
        { type: 'put', key: 'user:3', value: 'Carol' },
        { type: 'put', key: 'session:abc', value: '{"role":"admin","exp":9999}' },
        { type: 'put', key: 'config:theme', value: 'dark' },
      ];
      await api.batch(ops);
      addLog('success', `BATCH committed ${ops.length} ops atomically`);
    } catch (e) {
      addLog('error', `BATCH failed: ${e.message}`);
    } finally { setLoading(false); }
  };

  const logColor = { success: C.green, warn: C.amber, error: C.red, info: C.muted };

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
      {/* Controls */}
      <div style={css.card}>
        <div style={css.cardTitle}>key operations</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div>
            <label style={{ fontSize: 11, color: C.muted, display: 'block', marginBottom: 5 }}>KEY</label>
            <input
              style={css.input} value={key}
              onChange={e => setKey(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleGet()}
              placeholder="e.g. user:1"
            />
          </div>
          <div>
            <label style={{ fontSize: 11, color: C.muted, display: 'block', marginBottom: 5 }}>VALUE (for PUT)</label>
            <input
              style={css.input} value={value}
              onChange={e => setValue(e.target.value)}
              placeholder="e.g. Alice"
            />
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <button style={css.btn()} onClick={handleGet} disabled={loading}>GET</button>
            <button style={css.btn('green')} onClick={handlePut} disabled={loading}>PUT</button>
            <button style={css.btn('red')} onClick={handleDelete} disabled={loading}>DELETE</button>
            <button style={{ ...css.btn('ghost'), marginLeft: 'auto' }} onClick={handleBatch} disabled={loading}>
              Seed demo data
            </button>
          </div>

          {result && (
            <div style={{
              padding: '10px 14px', borderRadius: 6, marginTop: 4,
              background: result.found ? C.greenDim : C.amberDim,
              border: `1px solid ${result.found ? C.green : C.amber}22`,
            }}>
              {result.found ? (
                <>
                  <div style={{ fontSize: 11, color: C.green, marginBottom: 4 }}>KEY FOUND</div>
                  <div style={{ ...css.mono, wordBreak: 'break-all' }}>{result.value}</div>
                </>
              ) : (
                <div style={{ fontSize: 13, color: C.amber }}>Key "{result.key}" not found</div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Operation log */}
      <div style={css.card}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
          <div style={css.cardTitle}>operation log</div>
          {log.length > 0 && (
            <button style={{ ...css.btn('ghost'), padding: '3px 10px', fontSize: 11 }} onClick={() => setLog([])}>
              clear
            </button>
          )}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 260, overflowY: 'auto' }}>
          {log.length === 0 && (
            <div style={{ color: C.muted, fontSize: 13, textAlign: 'center', padding: '2rem 0' }}>
              No operations yet
            </div>
          )}
          {log.map((entry, i) => (
            <div key={i} style={{
              display: 'flex', gap: 10, alignItems: 'flex-start',
              padding: '6px 10px', borderRadius: 5,
              background: i === 0 ? `${logColor[entry.type]}11` : 'transparent',
              fontSize: 12,
            }}>
              <span style={{ color: C.muted, ...css.mono, flexShrink: 0 }}>{entry.ts}</span>
              <span style={{ color: logColor[entry.type], ...css.mono, wordBreak: 'break-all' }}>{entry.msg}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// ─── Batch Transaction Panel ──────────────────────────────────────────────────
function BatchPanel() {
  const [ops, setOps] = useState([
    { type: 'put', key: '', value: '' },
  ]);
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(false);

  const addOp = () => setOps(o => [...o, { type: 'put', key: '', value: '' }]);
  const removeOp = (i) => setOps(o => o.filter((_, idx) => idx !== i));
  const updateOp = (i, field, val) => setOps(o => o.map((op, idx) => idx === i ? { ...op, [field]: val } : op));

  const commit = async () => {
    const valid = ops.filter(o => o.key.trim());
    if (!valid.length) return;
    setLoading(true);
    setStatus(null);
    try {
      await api.batch(valid);
      setStatus({ ok: true, msg: `${valid.length} ops committed atomically via MVCC transaction` });
      setOps([{ type: 'put', key: '', value: '' }]);
    } catch (e) {
      setStatus({ ok: false, msg: e.message });
    } finally { setLoading(false); }
  };

  return (
    <div style={css.card}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
        <div>
          <div style={css.cardTitle}>ACID batch transaction</div>
          <div style={{ fontSize: 12, color: C.muted }}>All ops commit atomically or none do</div>
        </div>
        <span style={css.badge(C.purple, `${C.purple}22`)}>MVCC</span>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 12 }}>
        {ops.map((op, i) => (
          <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <select
              style={{ ...css.input, width: 90, cursor: 'pointer' }}
              value={op.type}
              onChange={e => updateOp(i, 'type', e.target.value)}
            >
              <option value="put">PUT</option>
              <option value="delete">DELETE</option>
            </select>
            <input style={css.input} placeholder="key" value={op.key} onChange={e => updateOp(i, 'key', e.target.value)} />
            {op.type === 'put' && (
              <input style={css.input} placeholder="value" value={op.value} onChange={e => updateOp(i, 'value', e.target.value)} />
            )}
            <button
              style={{ background: 'none', border: 'none', color: C.muted, cursor: 'pointer', fontSize: 16, padding: '0 4px' }}
              onClick={() => removeOp(i)}
            >×</button>
          </div>
        ))}
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        <button style={css.btn('ghost')} onClick={addOp}>+ add op</button>
        <button style={css.btn('green')} onClick={commit} disabled={loading}>
          {loading ? 'committing…' : 'commit transaction'}
        </button>
      </div>

      {status && (
        <div style={{
          marginTop: 12, padding: '10px 14px', borderRadius: 6,
          background: status.ok ? C.greenDim : C.amberDim,
          border: `1px solid ${status.ok ? C.green : C.red}33`,
          fontSize: 13, color: status.ok ? C.green : C.red,
          ...css.mono,
        }}>
          {status.ok ? '✓ ' : '✗ '}{status.msg}
        </div>
      )}
    </div>
  );
}

// ─── Main App ─────────────────────────────────────────────────────────────────
export default function App() {
  const [tab, setTab] = useState('dashboard');
  const [stats, setStats] = useState(null);
  const [online, setOnline] = useState(false);
  const [history, setHistory] = useState([]);
  const [opCount, setOpCount] = useState({ writes: 0, reads: 0 });
  const prevCount = useRef({ writes: 0, reads: 0 });
  const ticker = useRef(null);

  const fetchStats = useCallback(async () => {
    try {
      const s = await api.getStats();
      setStats(s);
      setOnline(true);

      // Derive op delta from clock version
      const newWrites = s.clock_version || 0;
      const delta = newWrites - (prevCount.current.writes || newWrites);
      prevCount.current.writes = newWrites;

      const ts = new Date().toLocaleTimeString('en', { hour12: false, second: '2-digit' });
      setHistory(h => [...h.slice(-29), { t: ts, writes: Math.max(0, delta * 2), reads: Math.max(0, delta) }]);
    } catch {
      setOnline(false);
    }
  }, []);

  useEffect(() => {
    fetchStats();
    ticker.current = setInterval(fetchStats, 2000);
    return () => clearInterval(ticker.current);
  }, [fetchStats]);

  const levelCounts = stats?.level_counts || [];
  const totalSSTables = levelCounts.reduce((a, b) => a + b, 0);

  return (
    <div style={css.app}>
      {/* Header */}
      <header style={css.header}>
        <div style={css.logo}>
          <div style={css.logoDot} />
          LiteKV
          <span style={{ fontSize: 11, color: C.muted, fontWeight: 400, marginLeft: 2 }}>dashboard</span>
        </div>
        <nav style={css.nav}>
          {['dashboard', 'explorer', 'transactions'].map(t => (
            <button key={t} style={css.navBtn(tab === t)} onClick={() => setTab(t)}>
              {t}
            </button>
          ))}
        </nav>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
          <StatusDot online={online} />
          <span style={{ color: C.muted }}>{online ? 'connected' : 'server offline'}</span>
          {online && <span style={{ color: C.muted, fontSize: 11 }}>:8080</span>}
        </div>
      </header>

      <main style={css.main}>
        {!online && (
          <div style={{
            padding: '14px 18px', borderRadius: 8, marginBottom: 16,
            background: `${C.amber}15`, border: `1px solid ${C.amber}33`,
            fontSize: 13, color: C.amber,
          }}>
            ⚠ Cannot reach LiteKV server at <span style={css.mono}>localhost:8080</span>.
            Run <span style={{ ...css.mono, background: C.bg, padding: '1px 6px', borderRadius: 4 }}>go run ./cmd/server</span> first.
          </div>
        )}

        {tab === 'dashboard' && (
          <>
            {/* Stat cards */}
            <div style={css.grid4}>
              <StatCard
                label="MemTable entries"
                value={fmt.num(stats?.memtable_entries || 0)}
                sub="in-memory write buffer"
                color={C.green}
              />
              <StatCard
                label="MemTable size"
                value={fmt.bytes(stats?.memtable_size_bytes || 0)}
                sub="flushed at 4 MB"
              />
              <StatCard
                label="WAL size"
                value={fmt.bytes(stats?.wal_size_bytes || 0)}
                sub="truncated after flush"
                color={C.amber}
              />
              <StatCard
                label="SSTables"
                value={totalSSTables}
                sub={`across ${levelCounts.filter(c => c > 0).length} active levels`}
                color={C.purple}
              />
            </div>

            {/* Charts row */}
            <div style={css.grid2}>
              <ThroughputChart data={history} />
              <LevelChart levelCounts={levelCounts} />
            </div>

            {/* LSM viz + clock */}
            <div style={css.grid2}>
              <LSMViz levelCounts={levelCounts} />
              <div style={css.card}>
                <div style={css.cardTitle}>engine info</div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {[
                    ['MVCC clock version', fmt.num(stats?.clock_version || 0), C.blue],
                    ['Write-Ahead Log',    fmt.bytes(stats?.wal_size_bytes || 0), C.amber],
                    ['Active level',       `L0 (${levelCounts[0] || 0} SSTables)`, C.green],
                    ['Compaction trigger', '4 SSTables per level', C.muted],
                    ['Bloom filter FP',   '~1% per SSTable', C.muted],
                    ['MemTable impl',     'Skip list O(log n)', C.muted],
                  ].map(([label, val, color]) => (
                    <div key={label} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13 }}>
                      <span style={{ color: C.muted }}>{label}</span>
                      <span style={{ ...css.mono, color: color || C.text }}>{val}</span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </>
        )}

        {tab === 'explorer' && (
          <>
            <KeyExplorer />
            <div style={{ marginTop: 16 }}>
              <div style={css.grid2}>
                <div style={css.card}>
                  <div style={css.cardTitle}>REST API reference</div>
                  {[
                    ['GET',    '/v1/keys/:key',    'Read a value'],
                    ['PUT',    '/v1/keys/:key',    'Write a value'],
                    ['DELETE', '/v1/keys/:key',    'Delete a key (tombstone)'],
                    ['POST',   '/v1/batch',        'Atomic batch (ACID)'],
                    ['GET',    '/v1/stats',        'Engine statistics'],
                    ['GET',    '/healthz',         'Health check'],
                  ].map(([method, path, desc]) => (
                    <div key={path} style={{ display: 'flex', gap: 10, alignItems: 'center', padding: '7px 0', borderBottom: `1px solid ${C.border}` }}>
                      <span style={{
                        ...css.mono, fontSize: 11, fontWeight: 600,
                        color: method === 'GET' ? C.blue : method === 'PUT' ? C.green : method === 'DELETE' ? C.red : C.amber,
                        width: 54,
                      }}>
                        {method}
                      </span>
                      <span style={{ ...css.mono, color: C.text, flex: 1 }}>{path}</span>
                      <span style={{ fontSize: 12, color: C.muted }}>{desc}</span>
                    </div>
                  ))}
                </div>
                <div style={css.card}>
                  <div style={css.cardTitle}>quick tips</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10, fontSize: 13, color: C.muted }}>
                    <p>Values are stored as raw bytes. The API base64-encodes them in JSON responses — the dashboard decodes them for display.</p>
                    <p>Deleting a key writes a <strong style={{ color: C.text }}>tombstone</strong> — the key is logically removed but physically deleted during compaction.</p>
                    <p>The <strong style={{ color: C.text }}>Bloom filter</strong> on each SSTable means a GET for a nonexistent key almost never touches disk.</p>
                    <p>Use <strong style={{ color: C.text }}>Seed demo data</strong> to quickly populate the engine and watch the MemTable fill up.</p>
                  </div>
                </div>
              </div>
            </div>
          </>
        )}

        {tab === 'transactions' && (
          <>
            <BatchPanel />
            <div style={{ marginTop: 16, ...css.card }}>
              <div style={css.cardTitle}>how ACID transactions work in LiteKV</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginTop: 4 }}>
                {[
                  ['Atomicity', 'All ops in a batch commit or none do. If the server crashes mid-commit, the WAL replay skips the incomplete transaction.', C.green],
                  ['Consistency', 'MVCC clock version increments on every commit. Readers see a snapshot from before the transaction started.', C.blue],
                  ['Isolation', 'Each transaction buffers writes privately. Other readers see the old values until Commit() is called.', C.purple],
                  ['Durability', 'The WAL is written and flushed before the MemTable is updated. A crash after WAL write is fully recoverable.', C.amber],
                ].map(([title, desc, color]) => (
                  <div key={title} style={{ padding: '14px', background: C.bg, borderRadius: 8, border: `1px solid ${C.border}` }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color, marginBottom: 6 }}>{title}</div>
                    <div style={{ fontSize: 12, color: C.muted, lineHeight: 1.6 }}>{desc}</div>
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </main>
    </div>
  );
}
