(function () {
  const { h, render } = preact;
  const { useState, useEffect, useMemo, useRef, useCallback } = preactHooks;
  const html = htm.bind(h);

  /* ---------- auth + api ---------- */

  class Unauthorized extends Error {}

  const authListeners = [];
  function subscribeAuth(cb) {
    authListeners.push(cb);
    return () => { const i = authListeners.indexOf(cb); if (i >= 0) authListeners.splice(i, 1); };
  }
  function setAuthed(v) { authListeners.forEach((cb) => cb(v)); }

  async function api(path, opts) {
    const res = await fetch('/admin/api' + path, opts);
    if (res.status === 401) { setAuthed(false); throw new Unauthorized(); }
    if (!res.ok) {
      let msg = res.statusText;
      try { msg = (await res.text()).trim() || msg; } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  let refreshVersion = 0;
  const refreshListeners = [];
  function bumpRefresh() {
    refreshVersion++;
    refreshListeners.forEach((cb) => cb(refreshVersion));
  }
  function useRefresh() {
    const [v, setV] = useState(refreshVersion);
    useEffect(() => {
      refreshListeners.push(setV);
      return () => { const i = refreshListeners.indexOf(setV); if (i >= 0) refreshListeners.splice(i, 1); };
    }, []);
    return v;
  }

  function useData(path, deps) {
    const refresh = useRefresh();
    const [state, setState] = useState({ loading: true, data: null, error: null });
    useEffect(() => {
      let cancelled = false;
      // Keep existing data during refreshes to avoid flicker; only show the
      // skeleton on the first load.
      setState((prev) => ({ ...prev, loading: !prev.data, error: null }));
      api(path).then(
        (data) => { if (!cancelled) setState({ loading: false, data, error: null }); },
        (error) => { if (!cancelled) setState({ loading: false, data: null, error }); }
      );
      return () => { cancelled = true; };
    }, [...(deps || []), refresh]);
    return state;
  }

  /* ---------- toasts ---------- */

  const toastListeners = [];
  function toast(message, type) {
    const id = Math.random().toString(36).slice(2);
    toastListeners.forEach((cb) => cb({ id, message, type: type || 'info' }));
    setTimeout(() => toastListeners.forEach((cb) => cb({ id, remove: true })), 4200);
  }

  function ToastHost() {
    const [items, setItems] = useState([]);
    useEffect(() => {
      const cb = (t) => setItems((prev) => {
        if (t.remove) return prev.filter((x) => x.id !== t.id);
        return [...prev, t];
      });
      toastListeners.push(cb);
      return () => { const i = toastListeners.indexOf(cb); if (i >= 0) toastListeners.splice(i, 1); };
    }, []);
    return html`
      <div class="toasts">
        ${items.map((t) => html`
          <div class="toast ${t.type === 'error' ? 'toast-error' : ''}" key=${t.id}>
            ${t.type === 'error' ? '✕' : '✓'} ${t.message}
          </div>`)}
      </div>`;
  }

  /* ---------- helpers ---------- */

  function fmtBytes(n) {
    if (n == null) return '—';
    if (n === 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, v = n;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i];
  }

  function fmtDate(s) {
    if (!s) return '—';
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString();
  }

  function short(s, n) {
    if (!s) return '—';
    return s.length > n ? s.slice(0, n) + '…' : s;
  }

  // Extract a usable domain hostname from a public URL. Returns '' for IPs,
  // localhost, and bare hostnames so the wizard never prefills an internal
  // address (e.g. a Tailscale IP) into the domain field.
  function domainHostname(urlStr) {
    if (!urlStr) return '';
    try {
      const h = new URL(urlStr).hostname;
      if (h === 'localhost' || h.includes(':') || !h.includes('.') || /^\d+(\.\d+){3}$/.test(h)) return '';
      return h;
    } catch (_) { return ''; }
  }

  /* ---------- icons ---------- */

  const svgProps = {
    width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none',
    stroke: 'currentColor', 'stroke-width': '2', 'stroke-linecap': 'round', 'stroke-linejoin': 'round'
  };

  const icons = {
    dashboard: html`<svg ...${svgProps}><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>`,
    accounts: html`<svg ...${svgProps}><path d="M17 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9.5" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>`,
    repo: html`<svg ...${svgProps}><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/><path d="M3 12c0 1.66 4 3 9 3s9-1.34 9-3"/></svg>`,
    blobs: html`<svg ...${svgProps}><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m21 15-5-5L5 21"/></svg>`,
    relay: html`<svg ...${svgProps}><circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3a15 15 0 0 1 0 18 15 15 0 0 1 0-18z"/></svg>`,
    events: html`<svg ...${svgProps}><path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/></svg>`,
    invite: html`<svg ...${svgProps}><path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2z"/><path d="M13 5v2"/><path d="M13 17v2"/><path d="M13 11v2"/></svg>`,
    email: html`<svg ...${svgProps}><rect x="2" y="4" width="20" height="16" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg>`,
    setup: html`<svg ...${svgProps}><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><path d="m22 4-10 10.01-3-3"/></svg>`,
    settings: html`<svg ...${svgProps}><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
    logout: html`<svg ...${svgProps}><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5"/><path d="M21 12H9"/></svg>`,
    bridge: html`<svg ...${svgProps}><path d="M9 17H7A5 5 0 0 1 7 7h2"/><path d="M15 7h2a5 5 0 1 1 0 10h-2"/><path d="M8 12h8"/></svg>`,
    back: html`<svg ...${svgProps}><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>`
  };

  /* ---------- routing ---------- */

  function parseRoute() {
    return (location.hash.replace(/^#/, '') || '/dashboard').split('/').filter(Boolean);
  }
  function navigate(path) { location.hash = path; }
  function useRoute() {
    const [route, setRoute] = useState(parseRoute());
    useEffect(() => {
      const onHash = () => setRoute(parseRoute());
      window.addEventListener('hashchange', onHash);
      return () => window.removeEventListener('hashchange', onHash);
    }, []);
    return route;
  }

  /* ---------- primitives ---------- */

  function Stat(props) {
    return html`
      <div class="stat">
        <div class="stat-label">${props.label}</div>
        <div class="stat-value">${props.value}</div>
        ${props.sub ? html`<div class="stat-sub">${props.sub}</div>` : null}
      </div>`;
  }

  function Badge(props) {
    return html`<span class="badge badge-${props.tone || 'muted'}"><span class="badge-dot"></span>${props.children}</span>`;
  }

  function ConfirmButton(props) {
    const [arm, setArm] = useState(false);
    useEffect(() => { if (arm) { const t = setTimeout(() => setArm(false), 3000); return () => clearTimeout(t); } }, [arm]);
    return html`
      <button
        class="btn btn-danger btn-sm"
        onClick=${() => {
          if (arm) { setArm(false); props.onConfirm(); } else { setArm(true); }
        }}
      >${arm ? (props.confirmLabel || 'Confirm') : props.label}</button>`;
  }

  function ErrorState(props) {
    return html`
      <div class="empty">
        <div class="empty-title">Something went wrong</div>
        <div>${props.message || 'Failed to load data.'}</div>
        <button class="btn btn-ghost" style="margin-top: 1rem" onClick=${() => location.reload()}>Reload</button>
      </div>`;
  }

  function SkeletonRows(props) {
    const n = props.n || 5;
    return html`
      <div class="table-wrap">
        <table><tbody>
          ${Array.from({ length: n }).map((_, i) => html`<tr key=${i}><td><div class="skeleton skeleton-text" style="width:${40 + (i % 4) * 30}%"></div></td><td><div class="skeleton skeleton-text" style="width:${60 - (i % 3) * 10}%"></div></td></tr>`)}
        </tbody></table>
      </div>`;
  }

  /* ---------- login ---------- */

  function Login() {
    const [token, setToken] = useState('');
    const [err, setErr] = useState('');
    const [loading, setLoading] = useState(false);

    async function submit(e) {
      e.preventDefault();
      if (!token) return;
      setLoading(true); setErr('');
      try {
        const res = await fetch('/admin/api/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ token })
        });
        if (res.status === 429) setErr('Too many attempts — wait a moment.');
        else if (res.status === 401) setErr('Invalid admin token.');
        else if (!res.ok) setErr('Login failed.');
        else { setAuthed(true); }
      } catch (_) {
        setErr('Could not reach the server.');
      }
      setLoading(false);
    }

    return html`
      <div class="login">
        <form class="login-card" onSubmit=${submit}>
          <div class="brand">
            <div class="brand-mark">P</div>
            <div class="brand-name">PocketPDS</div>
          </div>
          <p style="color: var(--text-muted); margin: 0">Sign in with your admin token.</p>
          <div class="field">
            <label class="field-label" for="token">Admin token</label>
            <input class="input" id="token" type="password" value=${token}
              onInput=${(e) => setToken(e.target.value)} placeholder="POCKETPDS_ADMIN_TOKEN"
              autocomplete="current-password" />
          </div>
          ${err ? html`<div style="color: var(--danger); font-size: 0.85rem">${err}</div>` : null}
          <button class="btn btn-primary" type="submit" disabled=${loading || !token}>
            ${loading ? html`<span class="spinner"></span>` : 'Sign in'}
          </button>
        </form>
      </div>`;
  }

  /* ---------- shell ---------- */

  const NAV = [
    { id: 'dashboard', label: 'Dashboard', icon: 'dashboard', path: '/dashboard' },
    { id: 'accounts', label: 'Accounts', icon: 'accounts', path: '/accounts' },
    { id: 'blobs', label: 'Blobs', icon: 'blobs', path: '/blobs' },
    { id: 'relays', label: 'Relays', icon: 'relay', path: '/relays' },
    { id: 'events', label: 'Events', icon: 'events', path: '/events' },
    { id: 'invites', label: 'Invite codes', icon: 'invite', path: '/invites' },
    { id: 'email', label: 'Email tokens', icon: 'email', path: '/email' },
    { id: 'setup', label: 'Setup wizard', icon: 'setup', path: '/setup' },
    { id: 'bridge', label: 'Bluesky bridge', icon: 'bridge', path: '/bridge' },
    { id: 'settings', label: 'Settings', icon: 'settings', path: '/settings' }
  ];

  function Shell() {
    const route = useRoute();
    const section = route[0] || 'dashboard';

    const titles = {
      dashboard: 'Dashboard', accounts: 'Accounts', account: 'Account',
      blobs: 'Blobs', relays: 'Relays', events: 'Events', invites: 'Invite codes',
      email: 'Email tokens', setup: 'Setup wizard', bridge: 'Bluesky bridge', settings: 'Settings'
    };

    async function logout() {
      try { await fetch('/admin/api/logout', { method: 'POST' }); } catch (_) {}
      setAuthed(false);
    }

    let view;
    switch (section) {
      case 'accounts': view = route[1] ? html`<${AccountDetail} did=${route[1]}/>` : html`<${Accounts}/>`; break;
      case 'blobs': view = html`<${Blobs}/>`; break;
      case 'relays': view = html`<${Relays}/>`; break;
      case 'events': view = html`<${Events}/>`; break;
      case 'invites': view = html`<${Invites}/>`; break;
      case 'email': view = html`<${EmailTokens}/>`; break;
      case 'setup': view = html`<${Setup}/>`; break;
      case 'bridge': view = html`<${Bridge}/>`; break;
      case 'settings': view = html`<${Settings} onLogout=${logout}/>`; break;
      default: view = html`<${Dashboard}/>`;
    }

    return html`
      <div class="app">
        <aside class="sidebar">
          <div class="brand">
            <div class="brand-mark">P</div>
            <div class="brand-name">PocketPDS</div>
          </div>
          ${NAV.map((item) => html`
            <button class="nav-item ${section === item.id ? 'active' : ''}"
              onClick=${() => navigate(item.path)}>
              <span class="nav-icon">${icons[item.icon]}</span>${item.label}
            </button>`)}
          <div class="sidebar-footer">
            <button class="nav-item" onClick=${logout}>
              <span class="nav-icon">${icons.logout}</span>Sign out
            </button>
          </div>
        </aside>
        <main class="main">
          <header class="topbar">
            <div class="topbar-title">${titles[section] || 'PocketPDS'}</div>
          </header>
          <div class="content">${view}</div>
        </main>
        <${ToastHost}/>
      </div>`;
  }

  /* ---------- dashboard ---------- */

  function Dashboard() {
    const { loading, data, error } = useData('/overview', []);
    if (loading) return html`<div class="grid grid-stats">${Array.from({length:6}).map((_,i)=>html`<div class="skeleton skeleton-stat" key=${i}></div>`)}</div>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const o = data;

    return html`
      <div class="fade-in">
        <div class="grid grid-stats" style="margin-bottom: 1.5rem">
          <${Stat} label="Accounts" value=${o.accounts.total} sub=${o.accounts.active + ' active'}/>
          <${Stat} label="Records" value=${o.records}/>
          <${Stat} label="Blobs" value=${o.blobs.count} sub=${fmtBytes(o.blobs.bytes)}/>
          <${Stat} label="Repo blocks" value=${o.blocks.count} sub=${fmtBytes(o.blocks.bytes)}/>
          <${Stat} label="Relays" value=${o.relays}/>
          <${Stat} label="Firehose events" value=${o.firehoseEvents}/>
        </div>

        ${!o.secretSet ? html`
          <div class="card" style="border-color: var(--warning); margin-bottom: 1.5rem">
            <div style="color: var(--warning); font-weight: 600">POCKETPDS_SECRET is not set</div>
            <div style="color: var(--text-muted); font-size: 0.85rem; margin-top: 0.25rem">
              Account keys are encrypted and tokens signed with an insecure development key. Set it before exposing this instance.
            </div>
          </div>` : null}

        <div class="card">
          <h3 class="section-title">Instance</h3>
          <dl class="kv">
            <dt>Version</dt><dd>${o.version}</dd>
            <dt>DID method</dt><dd>${o.didMethod}</dd>
            <dt>Service DID</dt><dd class="mono">${o.serviceDid || '—'}</dd>
            <dt>Public URL</dt><dd class="mono">${o.publicUrl}</dd>
            <dt>Listen address</dt><dd class="mono">${o.listenAddr}</dd>
            <dt>Database</dt><dd class="mono">${o.dbPath} <span style="color: var(--text-faint)">(${fmtBytes(o.dbSizeBytes)})</span></dd>
            <dt>Data directory</dt><dd class="mono">${o.dataDir}</dd>
            <dt>Invite required</dt><dd>${o.inviteRequired ? 'yes' : 'no'}</dd>
          </dl>
        </div>
      </div>`;
  }

  /* ---------- accounts ---------- */

  function Accounts() {
    const { loading, data, error } = useData('/accounts', []);
    if (loading) return html`<${SkeletonRows} n=6/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;

    const acts = data.accounts || [];
    if (acts.length === 0) {
      return html`<div class="empty"><div class="empty-title">No accounts yet</div><div>Create one with com.atproto.server.createAccount.</div></div>`;
    }

    return html`
      <div class="card fade-in">
        <div class="table-wrap">
          <table>
            <thead><tr><th>Handle</th><th>DID</th><th>Email</th><th>Records</th><th>Blobs</th><th>Status</th><th></th></tr></thead>
            <tbody>
              ${acts.map((a) => html`
                <tr class="clickable" onClick=${() => navigate('/accounts/' + a.did)}>
                  <td style="font-weight: 600">${a.handle}</td>
                  <td class="mono truncate" title=${a.did}>${short(a.did, 28)}</td>
                  <td>${a.email || ''}</td>
                  <td>${a.recordCount}</td>
                  <td>${a.blobCount}</td>
                  <td>${a.active ? html`<${Badge} tone="success">active<//>` : html`<${Badge} tone="danger">inactive<//>`}</td>
                  <td onClick=${(e) => e.stopPropagation()}>
                    <div style="display:flex; gap:0.25rem">
                      ${a.active
                        ? html`<button class="btn btn-ghost btn-sm" onClick=${() => doAccountAction(a.did, 'deactivate')}>Deactivate</button>`
                        : html`<button class="btn btn-ghost btn-sm" onClick=${() => doAccountAction(a.did, 'activate')}>Activate</button>`}
                    </div>
                  </td>
                </tr>`)}
            </tbody>
          </table>
        </div>
      </div>`;
  }

  async function doAccountAction(did, action) {
    try {
      await api('/accounts/' + did + '/' + action, { method: 'POST' });
      toast('Account ' + action + 'd', 'info');
      bumpRefresh();
    } catch (e) {
      toast(e.message || 'Action failed', 'error');
    }
  }

  /* ---------- account detail ---------- */

  function AccountDetail(props) {
    const did = props.did;
    const { loading, data, error } = useData('/accounts/' + did, [did]);
    const [tab, setTab] = useState('overview');

    if (loading) return html`<div class="grid grid-2"><div class="skeleton skeleton-stat"></div><div class="skeleton skeleton-stat"></div></div>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;

    const tabs = [
      { id: 'overview', label: 'Overview' },
      { id: 'records', label: 'Records' },
      { id: 'commits', label: 'Commits' },
      { id: 'blobs', label: 'Blobs' },
      { id: 'sessions', label: 'Sessions' },
      { id: 'apppass', label: 'App passwords' }
    ];

    return html`
      <div class="fade-in">
        <div style="display:flex; align-items:center; gap:0.75rem; margin-bottom:1rem">
          <button class="btn btn-ghost btn-sm" onClick=${() => navigate('/accounts')}>${icons.back} Accounts</button>
          <h2 style="font-size:1.25rem">${data.handle}</h2>
          ${data.active ? html`<${Badge} tone="success">active<//>` : html`<${Badge} tone="danger">inactive<//>`}
          <span style="margin-left:auto" class="mono" title=${did}>${short(did, 36)}</span>
        </div>

        <div class="tabs">
          ${tabs.map((t) => html`<button class="tab ${tab === t.id ? 'active' : ''}" onClick=${() => setTab(t.id)}>${t.label}</button>`)}
        </div>

        ${tab === 'overview' && html`<${AccountOverview} did=${did} data=${data}/>`}
        ${tab === 'records' && html`<${AccountRecords} did=${did} collections=${data.collections}/>`}
        ${tab === 'commits' && html`<${AccountCommits} did=${did}/>`}
        ${tab === 'blobs' && html`<${AccountBlobs} did=${did}/>`}
        ${tab === 'sessions' && html`<${AccountSessions} did=${did}/>`}
        ${tab === 'apppass' && html`<${AppPasswords} did=${did}/>`}

        <div style="margin-top:2rem; display:flex; gap:0.5rem; border-top:1px solid var(--border); padding-top:1.5rem">
          ${data.active
            ? html`<button class="btn btn-ghost" onClick=${() => doAccountAction(did, 'deactivate')}>Deactivate account</button>`
            : html`<button class="btn btn-ghost" onClick=${() => doAccountAction(did, 'activate')}>Activate account</button>`}
          <${ConfirmButton} label="Delete account" confirmLabel="Really delete?" onConfirm=${() => { doAccountAction(did, 'delete'); navigate('/accounts'); }}/>
        </div>
      </div>`;
  }

  function AccountOverview(props) {
    const a = props.data;
    return html`
      <div class="grid grid-2">
        <div class="card">
          <h3 class="section-title">Account</h3>
          <dl class="kv">
            <dt>Handle</dt><dd>${a.handle}</dd>
            <dt>DID</dt><dd class="mono">${a.did}</dd>
            <dt>Email</dt><dd>${a.email || '—'} ${a.emailConfirmed ? html`<${Badge} tone="success">confirmed<//>` : ''}</dd>
            <dt>Created</dt><dd>${fmtDate(a.createdAt)}</dd>
            <dt>Head commit</dt><dd class="mono">${short(a.head.cid, 34)} <span style="color:var(--text-faint)">rev ${a.head.rev}</span></dd>
            <dt>Blobs</dt><dd>${a.blobs.count} (${fmtBytes(a.blobs.bytes)})</dd>
            <dt>Sessions</dt><dd>${a.sessions}</dd>
            <dt>App passwords</dt><dd>${a.appPasswords}</dd>
          </dl>
        </div>
        <div class="card">
          <h3 class="section-title">Collections</h3>
          ${(a.collections || []).length === 0
            ? html`<div class="empty">No records yet.</div>`
            : html`<table><tbody>
                ${a.collections.map((c) => html`<tr><td class="mono">${c.collection}</td><td style="text-align:right">${c.count}</td></tr>`)}
              </tbody></table>`}
        </div>
      </div>

      <div class="card" style="margin-top:1.5rem">
        <h3 class="section-title">DID document</h3>
        <pre class="code-block">${JSON.stringify(a.didDoc, null, 2)}</pre>
      </div>`;
  }

  function AccountRecords(props) {
    const collections = props.collections || [];
    const [collection, setCollection] = useState(collections.length ? collections[0].collection : '');
    const [page, setPage] = useState(0);
    const [cursor, setCursor] = useState('');

    const path = '/accounts/' + props.did + '/records?collection=' + encodeURIComponent(collection) + '&limit=20' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '');
    const { loading, data, error } = useData(path, [props.did, collection, cursor]);

    if (collections.length === 0) return html`<div class="empty"><div class="empty-title">No collections</div><div>This account has no records yet.</div></div>`;

    return html`
      <div>
        <div class="field" style="max-width: 420px; margin-bottom: 1rem">
          <label class="field-label">Collection</label>
          <select class="input" value=${collection} onChange=${(e) => { setCollection(e.target.value); setCursor(''); setPage(0); }}>
            ${collections.map((c) => html`<option value=${c.collection}>${c.collection} (${c.count})</option>`)}
          </select>
        </div>

        ${loading ? html`<${SkeletonRows} n=6/>`
          : error ? html`<${ErrorState} message=${error.message}/>`
          : !data || !data.records || data.records.length === 0
            ? html`<div class="empty">No records in this collection.</div>`
            : html`
              <div class="card">
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Record key</th><th>CID</th><th></th></tr></thead>
                    <tbody>
                      ${data.records.map((r) => html`
                        <tr>
                          <td class="mono">${r.rkey}</td>
                          <td class="mono truncate" title=${r.cid}>${short(r.cid, 30)}</td>
                          <td><${RecordToggle} value=${r.value}/></td>
                        </tr>`)}
                    </tbody>
                  </table>
                </div>
              </div>`}

        ${data && data.cursor ? html`
          <div class="pager">
            <button class="btn btn-ghost btn-sm" disabled=${page === 0} onClick=${() => { setPage(0); setCursor(''); }}>First</button>
            <button class="btn btn-ghost btn-sm" onClick=${() => { setPage(page + 1); setCursor(data.cursor); }}>Next</button>
          </div>` : null}
      </div>`;
  }

  function RecordToggle(props) {
    const [open, setOpen] = useState(false);
    return html`
      <div>
        <button class="btn btn-ghost btn-sm" onClick=${() => setOpen(!open)}>${open ? 'Hide' : 'View'}</button>
        ${open ? html`<pre class="code-block" style="margin-top:0.5rem">${JSON.stringify(props.value, null, 2)}</pre>` : null}
      </div>`;
  }

  function AccountCommits(props) {
    const { loading, data, error } = useData('/accounts/' + props.did + '/commits?limit=100', [props.did]);
    if (loading) return html`<${SkeletonRows} n=6/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const commits = data.commits || [];
    if (commits.length === 0) return html`<div class="empty">No commits.</div>`;

    return html`
      <div class="card">
        <div class="timeline">
          ${commits.map((c) => html`
            <div class="timeline-item">
              <div class="tl-head">rev <span class="mono">${c.rev}</span></div>
              <div class="tl-sub">
                <div>commit <span class="mono">${short(c.cid, 40)}</span></div>
                <div>data root <span class="mono">${short(c.dataRoot, 40)}</span></div>
                ${c.prevCid ? html`<div>prev <span class="mono">${short(c.prevCid, 40)}</span></div>` : html`<div>prev — (genesis)</div>`}
                <div style="color:var(--text-faint)">${fmtDate(c.createdAt)}</div>
              </div>
            </div>`)}
        </div>
      </div>`;
  }

  function AccountBlobs(props) {
    return html`<${BlobTable} path=${'/accounts/' + props.did + '/blobs'}/>`;
  }

  function AccountSessions(props) {
    const { loading, data, error } = useData('/accounts/' + props.did + '/sessions', [props.did]);
    if (loading) return html`<${SkeletonRows} n=4/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const sessions = data.sessions || [];
    if (sessions.length === 0) return html`<div class="empty">No active sessions.</div>`;

    return html`
      <div class="card">
        <div class="table-wrap">
          <table>
            <thead><tr><th>Created</th><th>Expires</th><th>Type</th></tr></thead>
            <tbody>
              ${sessions.map((s) => html`
                <tr>
                  <td>${fmtDate(s.createdAt)}</td>
                  <td>${fmtDate(s.expiresAt)}</td>
                  <td>${s.appPassword ? html`<${Badge} tone="accent">app password<//>` : html`<${Badge}>session<//>`}</td>
                </tr>`)}
            </tbody>
          </table>
        </div>
      </div>`;
  }

  function AppPasswords(props) {
    const path = '/accounts/' + props.did + '/appPasswords';
    const { loading, data, error } = useData(path, [props.did]);
    const [name, setName] = useState('');
    const [busy, setBusy] = useState(false);
    const [created, setCreated] = useState(null);

    async function create() {
      if (!name.trim()) return;
      setBusy(true); setCreated(null);
      try {
        const res = await api(path, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: name.trim() })
        });
        setCreated(res);
        setName('');
        toast('App password created');
        bumpRefresh();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    async function revoke(n) {
      try {
        await api(path + '/revoke', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: n })
        });
        toast('App password revoked');
        bumpRefresh();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
    }

    return html`
      <div>
        <div class="card" style="display:flex; align-items:flex-end; gap:0.75rem; margin-bottom:1.5rem">
          <div class="field" style="max-width: 280px; flex:1">
            <label class="field-label">Name</label>
            <input class="input" value=${name} onInput=${(e) => setName(e.target.value)} placeholder="e.g. automation"/>
          </div>
          <button class="btn btn-primary" onClick=${create} disabled=${busy || !name.trim()}>
            ${busy ? html`<span class="spinner"></span>` : 'Create app password'}
          </button>
        </div>

        ${created ? html`
          <div class="card" style="border-color: var(--accent); margin-bottom:1.5rem">
            <div class="section-title">App password created</div>
            <div class="mono" style="font-size:1.05rem; letter-spacing:0.03em">${created.password}</div>
            <div style="color: var(--text-muted); font-size:0.82rem; margin-top:0.5rem">Copy it now — it will not be shown again.</div>
          </div>` : null}

        ${loading ? html`<${SkeletonRows} n=3/>`
          : error ? html`<${ErrorState} message=${error.message}/>`
          : !data || !data.passwords || data.passwords.length === 0
            ? html`<div class="empty">No app passwords for this account.</div>`
            : html`
              <div class="card">
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Name</th><th>Created</th><th></th></tr></thead>
                    <tbody>
                      ${data.passwords.map((p) => html`
                        <tr>
                          <td>${p.name}</td>
                          <td>${fmtDate(p.createdAt)}</td>
                          <td><${ConfirmButton} label="Revoke" confirmLabel="Revoke?" onConfirm=${() => revoke(p.name)}/></td>
                        </tr>`)}
                    </tbody>
                  </table>
                </div>
              </div>`}
      </div>`;
  }

  /* ---------- blobs ---------- */

  function Blobs() {
    return html`<${BlobTable} path="/blobs"/>`;
  }

  function BlobTable(props) {
    const [cursor, setCursor] = useState('');
    const path = props.path + (cursor ? (props.path.includes('?') ? '&' : '?') + 'cursor=' + encodeURIComponent(cursor) : '');
    const { loading, data, error } = useData(path, [props.path, cursor]);
    if (loading) return html`<${SkeletonRows} n=6/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const blobs = (data && data.blobs) || [];
    if (blobs.length === 0) return html`<div class="empty"><div class="empty-title">No blobs</div><div>Uploaded media will appear here.</div></div>`;

    return html`
      <div class="card fade-in">
        <div class="table-wrap">
          <table>
            <thead><tr><th>CID</th><th>Owner</th><th>Type</th><th>Size</th><th>Created</th></tr></thead>
            <tbody>
              ${blobs.map((b) => html`
                <tr>
                  <td class="mono truncate" title=${b.cid}>${short(b.cid, 32)}</td>
                  <td class="mono truncate" title=${b.did}>${short(b.did, 24)}</td>
                  <td>${b.mimeType || '—'}</td>
                  <td>${fmtBytes(b.size)}</td>
                  <td class="mono">${b.createdAt ? short(b.createdAt, 16) : '—'}</td>
                </tr>`)}
            </tbody>
          </table>
        </div>
        ${data && data.cursor ? html`
          <div class="pager">
            <button class="btn btn-ghost btn-sm" onClick=${() => setCursor(data.cursor)}>Next</button>
          </div>` : null}
      </div>`;
  }

  /* ---------- relays ---------- */

  function Relays() {
    const { loading, data, error } = useData('/relays', []);
    const [busy, setBusy] = useState(false);
    if (loading) return html`<${SkeletonRows} n=4/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const relays = (data && data.relays) || [];

    async function crawl() {
      setBusy(true);
      try {
        await api('/relays/crawl', { method: 'POST' });
        toast('Crawl requested from registered relays');
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    return html`
      <div class="fade-in">
        <div class="card" style="display:flex; align-items:center; justify-content:space-between; gap:0.75rem; margin-bottom:1.5rem">
          <div style="color:var(--text-muted)">${relays.length} registered relay(s).</div>
          <button class="btn btn-primary" onClick=${crawl} disabled=${busy || relays.length === 0}>
            ${busy ? html`<span class="spinner"></span>` : 'Request crawl'}
          </button>
        </div>

        ${relays.length === 0
          ? html`<div class="empty"><div class="empty-title">No relays</div><div>Relays that call com.atproto.sync.notifyOfUpdate are registered here.</div></div>`
          : html`
            <div class="card">
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Hostname</th><th>Registered</th></tr></thead>
                  <tbody>
                    ${relays.map((r) => html`<tr><td class="mono">${r.hostname}</td><td>${fmtDate(r.registeredAt)}</td></tr>`)}
                  </tbody>
                </table>
              </div>
            </div>`}
      </div>`;
  }

  /* ---------- events ---------- */

  function Events() {
    const [events, setEvents] = useState([]);
    const [connected, setConnected] = useState(false);

    useEffect(() => {
      const es = new EventSource('/admin/api/events?limit=50');
      es.onopen = () => setConnected(true);
      es.onerror = () => setConnected(false);
      es.onmessage = (e) => {
        try {
          const evt = JSON.parse(e.data);
          setEvents((prev) => [...prev.slice(-199), evt]);
        } catch (_) {}
      };
      return () => es.close();
    }, []);

    const list = events.slice().reverse();

    return html`
      <div class="fade-in">
        <div class="card" style="display:flex; align-items:center; justify-content:space-between; margin-bottom:1.5rem">
          <div style="display:flex; align-items:center; gap:0.5rem">
            <span class="badge-dot" style="background:${connected ? 'var(--success)' : 'var(--danger)'}"></span>
            <span style="font-weight:600">${connected ? 'Live' : 'Connecting…'}</span>
          </div>
          <span style="color:var(--text-muted); font-size:0.82rem">${events.length} event(s)</span>
        </div>

        ${list.length === 0
          ? html`<div class="empty"><div class="empty-title">No events yet</div><div>Repo commits, identity changes, and account events stream here in real time.</div></div>`
          : html`
            <div class="card">
              <div class="table-wrap">
                <table>
                  <thead><tr><th>#</th><th>Type</th><th>DID</th><th>Detail</th><th>Time</th></tr></thead>
                  <tbody>
                    ${list.map((e) => html`
                      <tr>
                        <td class="mono">${e.seq != null ? e.seq : ''}</td>
                        <td>${e.type === '#commit' ? html`<${Badge} tone="accent">commit<//>`
                          : e.type === '#identity' ? html`<${Badge} tone="success">identity<//>`
                          : e.type === '#account' ? html`<${Badge} tone="warning">account<//>`
                          : html`<${Badge}>${e.type}<//>`}</td>
                        <td class="mono truncate" title=${e.did || ''}>${short(e.did, 22)}</td>
                        <td class="mono" style="font-size:0.78rem">${eventDetail(e)}</td>
                        <td>${fmtDate(e.time)}</td>
                      </tr>`)}
                  </tbody>
                </table>
              </div>
            </div>`}
      </div>`;
  }

  function eventDetail(e) {
    switch (e.type) {
      case '#commit': return 'rev ' + short(e.rev, 12) + ' · ' + e.opCount + ' op(s)';
      case '#identity': return e.handle ? 'handle → ' + e.handle : '';
      case '#account': return e.active ? 'active' : 'inactive' + (e.status ? ' (' + e.status + ')' : '');
      case '#info': return e.name || '';
      default: return '';
    }
  }

  /* ---------- invites ---------- */

  function Invites() {
    const { loading, data, error } = useData('/inviteCodes', []);
    const [count, setCount] = useState(5);
    const [busy, setBusy] = useState(false);

    async function create() {
      setBusy(true);
      try {
        const res = await api('/inviteCodes', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ count: +count }) });
        toast('Created ' + (res.codes || []).length + ' code(s)');
        bumpRefresh();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    return html`
      <div class="fade-in">
        <div class="card" style="display:flex; align-items:flex-end; gap:0.75rem; margin-bottom:1.5rem">
          <div class="field" style="max-width:140px">
            <label class="field-label">Count</label>
            <input class="input" type="number" min="1" max="100" value=${count} onInput=${(e) => setCount(e.target.value)}/>
          </div>
          <button class="btn btn-primary" onClick=${create} disabled=${busy}>${busy ? html`<span class="spinner"></span>` : 'Create invite codes'}</button>
        </div>

        ${loading ? html`<${SkeletonRows} n=5/>`
          : error ? html`<${ErrorState} message=${error.message}/>`
          : !data || !data.codes || data.codes.length === 0
            ? html`<div class="empty"><div class="empty-title">No invite codes</div><div>Generate codes to gate account creation.</div></div>`
            : html`
              <div class="card">
                <div class="table-wrap">
                  <table>
                    <thead><tr><th>Code</th><th>Created by</th><th>Created</th><th>Status</th></tr></thead>
                    <tbody>
                      ${data.codes.map((c) => html`
                        <tr>
                          <td class="mono">${c.code}</td>
                          <td>${c.createdBy}</td>
                          <td>${fmtDate(c.createdAt)}</td>
                          <td>${c.available ? html`<${Badge} tone="success">available<//>` : html`<${Badge} tone="muted">used<//>`}</td>
                        </tr>`)}
                    </tbody>
                  </table>
                </div>
              </div>`}
      </div>`;
  }

  /* ---------- email tokens ---------- */

  function EmailTokens() {
    const { loading, data, error } = useData('/emailTokens', []);
    if (loading) return html`<${SkeletonRows} n=5/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;
    const tokens = (data && data.tokens) || [];
    if (tokens.length === 0) return html`<div class="empty"><div class="empty-title">No email tokens</div><div>Email confirmation and password-reset requests will appear here.</div></div>`;

    return html`
      <div class="card fade-in">
        <div class="table-wrap">
          <table>
            <thead><tr><th>Purpose</th><th>Email</th><th>DID</th><th>Expires</th><th>Status</th></tr></thead>
            <tbody>
              ${tokens.map((t) => html`
                <tr>
                  <td>${t.purpose}</td>
                  <td>${t.email}</td>
                  <td class="mono truncate" title=${t.did}>${short(t.did, 26)}</td>
                  <td>${fmtDate(t.expiresAt)}</td>
                  <td>${t.status === 'used' ? html`<${Badge} tone="muted">used<//>`
                    : t.status === 'expired' ? html`<${Badge} tone="warning">expired<//>`
                    : html`<${Badge} tone="success">pending<//>`}</td>
                </tr>`)}
            </tbody>
          </table>
        </div>
      </div>`;
  }

  /* ---------- bluesky bridge ---------- */

  function Bridge() {
    const { loading, data, error } = useData('/bridge', []);
    const [handle, setHandle] = useState('');
    const [appPassword, setAppPassword] = useState('');
    const [cfToken, setCfToken] = useState('');
    const [zone, setZone] = useState('alesr.me');
    const [domain, setDomain] = useState('');
    const [did, setDid] = useState('');
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState(null);

    useEffect(() => {
      if (data && data.handle) {
        setHandle(data.handle);
        setDomain(data.handle.replace(/^@/, ''));
      }
    }, [data]);

    if (loading) return html`<${SkeletonRows} n=4/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;

    async function save(e) {
      e.preventDefault();
      setBusy(true);
      try {
        await api('/bridge', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ handle, appPassword }) });
        toast('Bluesky bridge saved');
        setAppPassword('');
        bumpRefresh();
      } catch (err) { toast(err.message || 'Failed', 'error'); }
      setBusy(false);
    }

    async function setupDNS() {
      setBusy(true);
      try {
        await api('/bridge/dns', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ apiToken: cfToken, zone, domain, did }) });
        toast('DNS record created: _atproto.' + domain + ' → did=' + did);
      } catch (err) { toast(err.message || 'Failed', 'error'); }
      setBusy(false);
    }

    async function syncNow() {
      setBusy(true);
      setResult(null);
      try {
        const rep = await api('/bridge/sync', { method: 'POST' });
        setResult(rep);
      } catch (err) { toast(err.message || 'Sync failed', 'error'); }
      setBusy(false);
    }

    return html`
      <div class="fade-in">
        <div class="card">
          <h3 class="section-title">Bluesky account</h3>
          <div style="color:var(--text-muted); margin-bottom:1rem">
            Link a bsky.social account so posts are published to the network and archived back into your repo.
          </div>
          <form onSubmit=${save}>
            <label class="field-label">bsky.social handle</label>
            <input class="input" value=${handle} onInput=${(e) => setHandle(e.target.value)} placeholder="alesr.me"/>
            <label class="field-label">App password</label>
            <input class="input" type="password" value=${appPassword} onInput=${(e) => setAppPassword(e.target.value)} placeholder=${data && data.passwordSet ? '•••••• (leave blank to keep)' : 'create one at bsky.app/settings/app-passwords'}/>
            <div class="row" style="margin-top:0.75rem">
              <button class="btn btn-primary" type="submit" disabled=${busy}>${busy ? html`<span class="spinner"></span>` : 'Save'}</button>
              ${data && data.configured ? html`<${Badge} tone="success">configured<//>` : html`<${Badge} tone="warning">not configured<//>`}
            </div>
          </form>
        </div>

        <div class="card">
          <h3 class="section-title">Custom domain handle</h3>
          <div style="color:var(--text-muted); margin-bottom:1rem">
            To use your own domain as the bsky.social handle, add the <span class="mono">_atproto</span> TXT record. First change your handle on bsky.app to get the DID, then fill it in here.
          </div>
          <div class="grid grid-2">
            <div>
              <label class="field-label">Cloudflare API token</label>
              <input class="input" type="password" value=${cfToken} onInput=${(e) => setCfToken(e.target.value)}/>
            </div>
            <div>
              <label class="field-label">Zone</label>
              <input class="input" value=${zone} onInput=${(e) => setZone(e.target.value)}/>
            </div>
            <div>
              <label class="field-label">Domain (handle)</label>
              <input class="input" value=${domain} onInput=${(e) => setDomain(e.target.value)} placeholder="alesr.me"/>
            </div>
            <div>
              <label class="field-label">bsky DID (did:plc:…)</label>
              <input class="input mono" value=${did} onInput=${(e) => setDid(e.target.value)}/>
            </div>
          </div>
          <button class="btn btn-primary" style="margin-top:0.75rem" onClick=${setupDNS} disabled=${busy}>Set up DNS record</button>
        </div>

        <div class="card" style="display:flex; align-items:center; justify-content:space-between; gap:0.75rem">
          <div>
            <div style="font-weight:600">Sync</div>
            <div style="color:var(--text-muted); font-size:0.85rem">Publish local posts to bsky.social and archive bsky.social posts into this repo.</div>
          </div>
          <button class="btn btn-primary" onClick=${syncNow} disabled=${busy || !data || !data.configured}>${busy ? html`<span class="spinner"></span>` : 'Sync now'}</button>
        </div>

        ${result && html`
          <div class="card">
            <div class="row" style="gap:1rem">
              <div><div class="stat-label">Published</div><div class="stat-value">${result.published}</div></div>
              <div><div class="stat-label">Archived</div><div class="stat-value">${result.archived}</div></div>
              <div><div class="stat-label">Errors</div><div class="stat-value">${(result.errors || []).length}</div></div>
            </div>
            ${(result.errors || []).length > 0 && html`
              <ul style="margin-top:0.75rem; color:var(--danger); font-size:0.82rem">
                ${result.errors.slice(0, 10).map((e) => html`<li>${e}</li>`)}
              </ul>`}
          </div>`}
      </div>`;
  }

  /* ---------- setup ---------- */

  function Setup() {
    const [step, setStep] = useState(0);
    const { loading, error } = useData('/diagnostics', []);

    const steps = ['Domain', 'DNS', 'Restart', 'Account', 'Verify'];

    if (loading) return html`<${SkeletonRows} n=6/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;

    return html`
      <div class="fade-in">
        <div class="steps">
          ${steps.map((s, i) => html`
            <div class="step ${i === step ? 'active' : i < step ? 'done' : ''}" onClick=${() => setStep(i)}>
              <div class="step-num">${i + 1}</div>
              <div class="step-label">${s}</div>
            </div>`)}
        </div>

        ${step === 0 && html`<${SetupDomain} onDone=${() => setStep(1)}/>`}
        ${step === 1 && html`<${SetupDNS} onDone=${() => setStep(2)}/>`}
        ${step === 2 && html`<${SetupRestart} onDone=${() => setStep(3)}/>`}
        ${step === 3 && html`<${SetupAccount} onDone=${() => setStep(4)}/>`}
        ${step === 4 && html`<${SetupVerify}/>`}
      </div>`;
  }

  function SetupDomain(props) {
    const { data } = useData('/settings', []);
    const [hostname, setHostname] = useState('');
    const [busy, setBusy] = useState(false);

    useEffect(() => {
      if (data && data.settings && !hostname) {
        setHostname(domainHostname(data.settings.publicUrl));
      }
    }, [data]);

    async function submit() {
      if (!hostname) return;
      setBusy(true);
      try {
        const current = (data && data.settings) || {};
        await api('/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...current, publicUrl: 'https://' + hostname, didMethod: 'web' }) });
        toast('Public URL set to https://' + hostname);
        bumpRefresh();
        props.onDone();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    return html`
      <div class="card">
        <h3 class="section-title">1 · Your domain</h3>
        <p style="color:var(--text-muted); margin-top:0">This becomes your PDS hostname and your account handle (<span class="mono">did:web</span>).</p>
        <div class="field" style="max-width:460px">
          <label class="field-label">Hostname</label>
          <input class="input mono" value=${hostname} onInput=${(e) => setHostname(e.target.value)} placeholder="pds.example.com"/>
        </div>
        <div style="margin-top:1rem">
          <button class="btn btn-primary" onClick=${submit} disabled=${busy || !hostname}>${busy ? html`<span class="spinner"></span>` : 'Save & continue'}</button>
        </div>
      </div>`;
  }

  function SetupDNS(props) {
    const { data } = useData('/settings', []);
    const diag = useData('/diagnostics', []);
    const [apiToken, setApiToken] = useState('');
    const [zone, setZone] = useState('');
    const [hostname, setHostname] = useState('');
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState(null);

    useEffect(() => {
      if (data && data.settings && !hostname) {
        setHostname(domainHostname(data.settings.publicUrl));
      }
    }, [data]);

    useEffect(() => {
      if (hostname && !zone) {
        const parts = hostname.split('.');
        if (parts.length >= 2) setZone(parts.slice(-2).join('.'));
      }
    }, [hostname]);

    async function apply() {
      setBusy(true); setResult(null);
      try {
        const res = await api('/cloudflare/apply', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ apiToken, zone, hostname }) });
        setResult(res);
        toast('Tunnel provisioned');
        bumpRefresh();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    const tunnelInstalled = !!(diag.data && diag.data.tunnelInstalled);

    return html`
      <div class="card" style="margin-bottom:1.5rem">
        <h3 class="section-title">2 · DNS — Cloudflare Tunnel</h3>
        <p style="color:var(--text-muted); margin-top:0; font-size:0.85rem">
          Creates a Cloudflare Tunnel (HTTPS handled by Cloudflare, no public IP needed) and the <span class="mono">_atproto</span> TXT record.
        </p>
        ${!tunnelInstalled ? html`<div style="margin-bottom:0.75rem; padding:0.6rem 0.75rem; border-radius:var(--radius-sm); background:var(--warning-soft); color:var(--warning); font-size:0.85rem">cloudflared not detected — run <code>sudo pocketpds tunnel install</code> on the server first.</div>` : null}
        <div class="form-grid">
          <div class="field full"><label class="field-label">API token</label><input class="input" type="password" value=${apiToken} onInput=${(e) => setApiToken(e.target.value)} placeholder="Cloudflare API token (Cloudflare Tunnel + DNS edit)"/></div>
          <div class="field"><label class="field-label">Zone</label><input class="input mono" value=${zone} onInput=${(e) => setZone(e.target.value)} placeholder="example.com"/></div>
          <div class="field"><label class="field-label">Hostname</label><input class="input mono" value=${hostname} onInput=${(e) => setHostname(e.target.value)} placeholder="pds.example.com"/></div>
        </div>
        <div style="margin-top:1rem; display:flex; gap:0.75rem">
          <button class="btn btn-primary" onClick=${apply} disabled=${busy || !apiToken || !zone || !hostname}>${busy ? html`<span class="spinner"></span>` : 'Provision tunnel & DNS'}</button>
          <button class="btn btn-ghost" onClick=${props.onDone}>Continue →</button>
        </div>
        ${result ? html`
          <div style="margin-top:1rem; font-size:0.85rem">
            <div style="color:var(--success)">Tunnel <span class="mono">${result.tunnelId}</span> created</div>
            <div>CNAME <span class="mono">${result.cname}</span> → tunnel · TXT <span class="mono">${result.txtRecord}</span> = <span class="mono">${result.txtContent}</span></div>
            ${result.bootstrapCommand ? html`<div style="color:var(--warning)">Finish by running: <code>${result.bootstrapCommand}</code></div>` : html`<div style="color:var(--success)">Tunnel running</div>`}
          </div>` : null}
      </div>

      <div class="card">
        <h3 class="section-title">2 · DNS — manual (any provider)</h3>
        <p style="color:var(--text-muted); margin-top:0; font-size:0.85rem">If you already run a Cloudflare Tunnel (or another reverse proxy), point these records at it, then continue.</p>
        <table>
          <thead><tr><th>Type</th><th>Name</th><th>Content</th></tr></thead>
          <tbody>
            <tr><td class="mono">CNAME</td><td class="mono">${hostname || 'pds.example.com'}</td><td class="mono">&lt;tunnel-id&gt;.cfargotunnel.com</td></tr>
            <tr><td class="mono">TXT</td><td class="mono">_atproto.${hostname || 'pds.example.com'}</td><td class="mono">did=did:web:${hostname || 'pds.example.com'}</td></tr>
          </tbody>
        </table>
        <div style="margin-top:1rem"><button class="btn btn-ghost" onClick=${props.onDone}>Done →</button></div>
      </div>`;
  }

  function SetupRestart(props) {
    const { data } = useData('/settings', []);
    const [restarting, setRestarting] = useState(false);
    const [restarted, setRestarted] = useState(false);

    const publicUrl = (data && data.settings && data.settings.publicUrl || '').replace(/\/$/, '');

    async function doRestart() {
      setRestarting(true);
      try {
        await api('/restart', { method: 'POST' });
        setRestarting(false);
        setRestarted(true);
        toast('Restarting…');
      } catch (e) {
        toast('Could not self-restart — restart manually', 'error');
        setRestarting(false);
      }
    }

    return html`
      <div class="card">
        <h3 class="section-title">3 · Apply & restart</h3>
        <p style="color:var(--text-muted); margin-top:0">Settings changes (public URL, DID method) take effect after a restart.</p>
        ${restarted ? html`
          <div style="padding:0.6rem 0.75rem; border-radius:var(--radius-sm); background:var(--success-soft); color:var(--success); font-size:0.9rem">
            Restarting. Once it's back up, open <span class="mono">${publicUrl}/admin</span> and log in again — your current (HTTP) session will no longer work now that HTTPS is enforced.
          </div>
          <div style="margin-top:1rem"><button class="btn btn-ghost" onClick=${props.onDone}>I've reopened it →</button></div>`
        : html`
          <div style="display:flex; gap:0.75rem">
            <button class="btn btn-primary" onClick=${doRestart} disabled=${restarting}>${restarting ? html`<span class="spinner"></span>` : 'Apply & restart'}</button>
            <button class="btn btn-ghost" onClick=${props.onDone}>I restarted manually →</button>
          </div>`}
      </div>`;
  }

  function SetupAccount(props) {
    const { data } = useData('/settings', []);
    const [handle, setHandle] = useState('');
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [busy, setBusy] = useState(false);
    const [done, setDone] = useState(false);

    useEffect(() => {
      if (data && data.settings && !handle) {
        setHandle(domainHostname(data.settings.publicUrl));
      }
    }, [data]);

    async function submit() {
      setBusy(true);
      try {
        const res = await fetch('/xrpc/com.atproto.server.createAccount', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ handle, email, password })
        });
        if (!res.ok) { const t = await res.text(); throw new Error(t); }
        setDone(true);
        toast('Account created: ' + handle);
        bumpRefresh();
      } catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    if (done) {
      return html`
        <div class="card">
          <div class="section-title">Account created ✓</div>
          <p style="color:var(--text-muted); margin-top:0"><span class="mono">${handle}</span> is live. Continue to verify.</p>
          <button class="btn btn-primary" onClick=${props.onDone}>Verify →</button>
        </div>`;
    }

    return html`
      <div class="card">
        <h3 class="section-title">4 · Create your first account</h3>
        <p style="color:var(--text-muted); margin-top:0; font-size:0.85rem">Your handle equals your domain.</p>
        <div class="form-grid">
          <div class="field full"><label class="field-label">Handle</label><input class="input mono" value=${handle} onInput=${(e) => setHandle(e.target.value)}/></div>
          <div class="field"><label class="field-label">Email</label><input class="input" value=${email} onInput=${(e) => setEmail(e.target.value)}/></div>
          <div class="field"><label class="field-label">Password</label><input class="input" type="password" value=${password} onInput=${(e) => setPassword(e.target.value)}/></div>
        </div>
        <div style="margin-top:1rem"><button class="btn btn-primary" onClick=${submit} disabled=${busy || !handle || !password}>${busy ? html`<span class="spinner"></span>` : 'Create account'}</button></div>
      </div>`;
  }

  function SetupVerify() {
    const { data } = useData('/settings', []);
    const publicUrl = ((data && data.settings && data.settings.publicUrl) || '').replace(/\/$/, '');
    const [checks, setChecks] = useState([]);
    const [loading, setLoading] = useState(true);
    const [run, setRun] = useState(0);

    useEffect(() => {
      const defs = [
        { id: 'health', label: 'Health endpoint', url: publicUrl + '/xrpc/_health' },
        { id: 'did-doc', label: 'DID document', url: publicUrl + '/.well-known/did.json' },
        { id: 'handle', label: 'Handle resolution', url: publicUrl + '/.well-known/atproto-did' },
        { id: 'describe', label: 'describeServer', url: publicUrl + '/xrpc/com.atproto.server.describeServer' }
      ];
      let cancelled = false;
      setLoading(true);
      (async () => {
        const results = await Promise.all(defs.map(async (c) => {
          try { const res = await fetch(c.url); return { ...c, ok: res.ok }; }
          catch (e) { return { ...c, ok: false, err: e.message }; }
        }));
        if (!cancelled) { setChecks(results); setLoading(false); }
      })();
      return () => { cancelled = true; };
    }, [publicUrl, run]);

    return html`
      <div class="card">
        <h3 class="section-title">5 · Verify</h3>
        <p style="color:var(--text-muted); margin-top:0">Checking <span class="mono">${publicUrl}</span>…</p>
        ${loading
          ? html`<div class="skeleton skeleton-text" style="width:60%"></div><div class="skeleton skeleton-text" style="width:80%"></div>`
          : checks.map((c) => html`
            <div class="check">
              <div class="check-status ${c.ok ? 'ok' : 'bad'}">${c.ok ? '✓' : '✕'}</div>
              <div class="check-body">
                <div class="check-title">${c.label}</div>
                <div class="check-detail"><code>${c.url}</code>${c.err ? ' — ' + c.err : ''}</div>
              </div>
            </div>`)}
        <div style="margin-top:1rem"><button class="btn btn-ghost" onClick=${() => setRun((r) => r + 1)}>Re-check</button></div>
      </div>`;
  }

  /* ---------- settings ---------- */

  function Settings(props) {
    const { loading, data, error } = useData('/settings', []);
    const [form, setForm] = useState(null);
    const [busy, setBusy] = useState(false);
    const [restarting, setRestarting] = useState(false);

    useEffect(() => { if (data && data.settings) setForm(data.settings); }, [data]);

    function set(key, value) { setForm((p) => ({ ...p, [key]: value })); }

    async function persist() {
      await api('/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form) });
    }

    async function save() {
      setBusy(true);
      try { await persist(); toast('Settings saved — restart to apply'); bumpRefresh(); }
      catch (e) { toast(e.message || 'Failed', 'error'); }
      setBusy(false);
    }

    async function applyAndRestart() {
      setRestarting(true);
      try {
        await persist();
        toast('Restarting…');
        setTimeout(async () => { try { await api('/restart', { method: 'POST' }); } catch (_) {} }, 300);
      } catch (e) { toast(e.message || 'Failed', 'error'); setRestarting(false); }
    }

    if (loading || !form) return html`<${SkeletonRows} n=8/>`;
    if (error) return html`<${ErrorState} message=${error.message}/>`;

    return html`
      <div class="fade-in">
        ${data.restartRequired ? html`
          <div class="card" style="border-color: var(--warning); margin-bottom:1.5rem">
            <div style="color:var(--warning); font-weight:600">Restart required</div>
            <div style="color:var(--text-muted); font-size:0.85rem; margin-top:0.25rem">Some settings changed and will apply after a restart.</div>
          </div>` : null}

        ${!data.secretSet ? html`
          <div class="card" style="border-color: var(--danger); margin-bottom:1.5rem">
            <div style="color:var(--danger); font-weight:600">POCKETPDS_SECRET is not set</div>
            <div style="color:var(--text-muted); font-size:0.85rem; margin-top:0.25rem">Account keys are encrypted with an insecure development key. Set it in the environment.</div>
          </div>` : null}

        <div class="card" style="margin-bottom:1.5rem">
          <h3 class="section-title">Identity</h3>
          <div class="form-grid">
            <div class="field full">
              <label class="field-label">Public URL</label>
              <input class="input mono" value=${form.publicUrl} onInput=${(e) => set('publicUrl', e.target.value)} placeholder="https://pds.example.com"/>
            </div>
            <div class="field">
              <label class="field-label">DID method</label>
              <select class="input" value=${form.didMethod} onChange=${(e) => set('didMethod', e.target.value)}>
                <option value="web">web (did:web)</option>
                <option value="plc">plc (did:plc)</option>
              </select>
            </div>
            <div class="field">
              <label class="field-label">Service DID</label>
              <input class="input mono" value=${form.serviceDid} onInput=${(e) => set('serviceDid', e.target.value)} placeholder="did:... (optional)"/>
            </div>
            <div class="field">
              <label class="field-label">Invite required</label>
              <div class="checkbox-row">
                <input type="checkbox" checked=${form.inviteRequired} onChange=${(e) => set('inviteRequired', e.target.checked)}/>
                <span style="font-size:0.85rem; color:var(--text-muted)">Require an invite code to sign up</span>
              </div>
            </div>
            <div class="field">
              <label class="field-label">Admin token</label>
              <input class="input mono" type="password" value=${form.adminToken} onInput=${(e) => set('adminToken', e.target.value)}/>
            </div>
          </div>
        </div>

        <div class="card" style="margin-bottom:1.5rem">
          <h3 class="section-title">Email (SMTP)</h3>
          <div class="form-grid">
            <div class="field"><label class="field-label">Host</label><input class="input" value=${form.smtpHost} onInput=${(e) => set('smtpHost', e.target.value)} placeholder="smtp.example.com"/></div>
            <div class="field"><label class="field-label">Port</label><input class="input" value=${form.smtpPort} onInput=${(e) => set('smtpPort', e.target.value)}/></div>
            <div class="field"><label class="field-label">User</label><input class="input" value=${form.smtpUser} onInput=${(e) => set('smtpUser', e.target.value)}/></div>
            <div class="field"><label class="field-label">Password</label><input class="input" type="password" value=${form.smtpPass} onInput=${(e) => set('smtpPass', e.target.value)}/></div>
            <div class="field full"><label class="field-label">From address</label><input class="input" value=${form.smtpFrom} onInput=${(e) => set('smtpFrom', e.target.value)} placeholder="pds@example.com"/></div>
          </div>
        </div>

        <div class="card" style="display:flex; align-items:center; justify-content:space-between; gap:0.75rem; margin-bottom:1.5rem">
          <div style="display:flex; align-items:center; gap:0.5rem">
            <button class="btn btn-ghost" onClick=${save} disabled=${busy}>${busy ? html`<span class="spinner"></span>` : 'Save settings'}</button>
            <span style="color:var(--text-faint); font-size:0.8rem">Changes apply on restart.</span>
          </div>
          <button class="btn btn-primary" onClick=${applyAndRestart} disabled=${restarting}>${restarting ? html`<span class="spinner"></span>` : 'Apply & restart'}</button>
        </div>

        <div class="card">
          <h3 class="section-title">Session</h3>
          <button class="btn btn-danger" onClick=${props.onLogout}>Sign out</button>
        </div>
      </div>`;
  }

  /* ---------- boot ---------- */

  function App() {
    const [authed, setAuthedState] = useState(undefined);
    useEffect(() => subscribeAuth(setAuthedState), []);
    useEffect(() => {
      api('/me').then(() => setAuthed(true)).catch(() => {});
    }, []);

    if (authed === undefined) {
      return html`<div class="login"><div class="login-card"><span class="spinner"></span></div></div>`;
    }
    if (!authed) return html`<${Login}/>`;
    return html`<${Shell}/>`;
  }

  render(html`<${App}/>`, document.getElementById('root'));
})();
