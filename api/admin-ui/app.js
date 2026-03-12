// ============================================================================
// TempMail Admin — Application Logic
// ============================================================================

let TOKEN = '';
let USERNAME = '';
const BASE = location.origin;
const SK = 'tm_admin_token';
const UK = 'tm_admin_user';
const PER_PAGE = 30;
let mboxPage = 0, msgPage = 0, auditPage = 0, domPage = 0, nodePage = 0, filterPage = 0, keyPage = 0;
let _dt = null;

// ── Session Management ──
function getToken() { return localStorage.getItem(SK) || sessionStorage.getItem(SK) || '' }
function getUser() { return localStorage.getItem(UK) || sessionStorage.getItem(UK) || '' }
function saveSession(token, user, remember) {
  const s = remember ? localStorage : sessionStorage;
  s.setItem(SK, token); s.setItem(UK, user);
}
function clearSession() {
  localStorage.removeItem(SK); localStorage.removeItem(UK);
  sessionStorage.removeItem(SK); sessionStorage.removeItem(UK);
}

(function () {
  const t = getToken(), u = getUser();
  if (t && u) { TOKEN = t; USERNAME = u; verify() }
})();

async function verify() {
  try {
    const r = await fetch(BASE + '/admin/dashboard', { headers: { 'Authorization': 'Bearer ' + TOKEN } });
    if (r.ok) { showApp(); loadDash() }
    else { clearSession(); TOKEN = ''; USERNAME = ''; showLogin() }
  } catch (e) { showApp() }
}

async function doLogin(e) {
  e.preventDefault();
  const u = document.getElementById('userIn').value.trim();
  const p = document.getElementById('passIn').value;
  if (!u || !p) { showErr('Please enter username and password'); return }
  const btn = document.getElementById('loginBtn');
  btn.disabled = true; btn.textContent = 'Signing in...';
  try {
    const r = await fetch(BASE + '/admin/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p })
    });
    const d = await r.json();
    if (r.ok && d.token) {
      TOKEN = d.token; USERNAME = d.username;
      saveSession(TOKEN, USERNAME, document.getElementById('remMe').checked);
      hideErr(); showApp(); loadDash(); startSSE();
    } else {
      showErr(d.error || 'Login failed');
      document.getElementById('passIn').value = '';
      document.getElementById('passIn').focus()
    }
  } catch (err) { showErr('Cannot connect to server') }
  finally { btn.disabled = false; btn.textContent = 'Sign In' }
}

function showErr(msg) { const el = document.getElementById('loginErr'); el.textContent = msg; el.style.display = 'block' }
function hideErr() { document.getElementById('loginErr').style.display = 'none' }
function logout() { TOKEN = ''; USERNAME = ''; clearSession(); stopSSE(); showLogin() }
function showLogin() { document.getElementById('loginScreen').style.display = 'flex'; document.getElementById('appW').classList.remove('on') }
function showApp() {
  document.getElementById('loginScreen').style.display = 'none';
  document.getElementById('appW').classList.add('on');
  document.getElementById('usrLabel').textContent = USERNAME;
}

// ── API Helper ──
async function api(p, m = 'GET', b = null) {
  const o = { method: m, headers: { 'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json' } };
  if (b) o.body = JSON.stringify(b);
  const r = await fetch(BASE + '/admin' + p, o);
  if (r.status === 401 || r.status === 403) { logout(); toast('Session expired', 'e'); throw new Error('Session expired') }
  
  const data = await r.json();
  if (!r.ok) {
    const errMsg = data.error?.message || data.error || data.message || `Error ${r.status}`;
    throw new Error(errMsg);
  }
  return data;
}

// ── UI Utilities ──
function toast(m, t = 's') { const d = document.createElement('div'); d.className = 'toast toast-' + t; d.textContent = m; document.body.appendChild(d); setTimeout(() => d.remove(), 3000) }
function dSearch(fn) { clearTimeout(_dt); _dt = setTimeout(() => { fn(true) }, 300) }

function tab(n, b) {
  document.querySelectorAll('.nav button').forEach(t => t.classList.remove('on'));
  document.querySelectorAll('.pn').forEach(p => p.classList.remove('on'));
  document.getElementById('pn-' + n).classList.add('on'); if (b) b.classList.add('on');
  _sseActiveTab = n;
  const ld = { dash: loadDash, dom: () => loadDom(true), node: () => loadNodes(true), filter: () => loadFilters(true), mbox: () => loadMbox(true), msg: () => loadMsg(true), apikey: () => loadAPIKeys(true), audit: () => loadAudit(true), set: loadSet };
  if (ld[n]) ld[n]()
}

function fDate(s) { if (!s) return '—'; const d = new Date(s); return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }) }
function fTime(s) { if (!s) return '—'; const d = new Date(s); return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }) + ' ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }) }
function fNum(n) { return (n || 0).toLocaleString() }
function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML }

// RFC 2047 MIME encoded-word decoder (for old DB subjects like =?UTF-8?B?...?=)
function decodeMIME(str) {
  if (!str || !str.includes('=?')) return str;
  try {
    return str.replace(/=\?([^?]+)\?([BQ])\?([^?]*)\?=/gi, function(match, charset, encoding, data) {
      if (encoding.toUpperCase() === 'B') {
        // Base64
        const bytes = atob(data);
        const uint8 = new Uint8Array(bytes.length);
        for (let i = 0; i < bytes.length; i++) uint8[i] = bytes.charCodeAt(i);
        return new TextDecoder(charset).decode(uint8);
      } else {
        // Quoted-Printable
        const decoded = data.replace(/_/g, ' ').replace(/=([0-9A-F]{2})/gi, function(m, hex) {
          return String.fromCharCode(parseInt(hex, 16));
        });
        const uint8 = new Uint8Array(decoded.length);
        for (let i = 0; i < decoded.length; i++) uint8[i] = decoded.charCodeAt(i);
        return new TextDecoder(charset).decode(uint8);
      }
    }).replace(/\s+/g, ' ').trim();
  } catch (e) { return str; }
}

// ── Skeleton Loading (proper skeleton rows matching table layouts) ──
const SKEL_COLS = {
  domT:    ['w-lg','w-md','w-badge','w-sm','w-sm','w-btn w-btn w-btn'],
  nodeT:   ['w-md','w-md','w-sm','w-xs','w-badge','w-btn w-btn'],
  filterT: ['w-lg','w-badge','w-md','w-sm','w-btn w-btn'],
  mboxT:   ['w-lg','w-badge','w-sm','w-sm','w-btn'],
  msgT:    ['w-xs','w-md','w-lg','w-md','w-badge','w-badge','w-sm','w-sm','w-btn w-btn'],
  keyT:    ['w-md','w-sm','w-sm','w-xs','w-badge','w-sm','w-btn w-btn'],
  auditT:  ['w-sm','w-md','w-sm','w-sm','w-sm'],
};

function ldg(id) {
  const cols = SKEL_COLS[id] || ['w-lg','w-md','w-sm','w-sm','w-xs'];
  let rows = '';
  for (let r = 0; r < 5; r++) {
    rows += '<tr class="skel-row">';
    for (const c of cols) {
      if (c.includes(' ')) {
        // Multiple bars (action buttons)
        const bars = c.split(' ').map(b => `<div class="skel-bar ${b}"></div>`).join('');
        rows += `<td><div class="skel-act">${bars}</div></td>`;
      } else {
        rows += `<td><div class="skel-bar ${c}"></div></td>`;
      }
    }
    rows += '</tr>';
  }
  document.getElementById(id).innerHTML = rows;
}

function empty(id, msg) { document.getElementById(id).innerHTML = `<tr><td colspan="10"><div class="empty"><div class="ic">📭</div><p>${msg}</p></div></td></tr>` }

// Dashboard skeleton
function skelDash() {
  // Hero stats skeleton
  let heroH = '';
  for (let i = 0; i < 6; i++) heroH += '<div class="skel-hero"><div class="skel-bar"></div><div class="skel-bar"></div></div>';
  document.getElementById('statsG').innerHTML = heroH;
  // Service status skeleton
  let svcH = '';
  for (let i = 0; i < 5; i++) svcH += '<div class="skel-st"><div class="skel-dot"></div><div class="skel-st-lines"><div class="skel-bar w-lg"></div><div class="skel-bar w-sm"></div></div></div>';
  document.getElementById('sysSt').innerHTML = svcH;
  // Metrics skeleton
  let metH = '<div class="sg">';
  for (let i = 0; i < 8; i++) metH += '<div class="skel-card"><div class="skel-bar"></div><div class="skel-bar"></div></div>';
  metH += '</div>';
  document.getElementById('metricsBody').innerHTML = metH;
}

function pgUI(id, page, total, perPage, fn) {
  const pages = Math.ceil(total / perPage);
  if (pages <= 1) { document.getElementById(id).innerHTML = ''; return }
  const maxShow = 7; let start = Math.max(0, page - 3), end = Math.min(pages, start + maxShow);
  if (end - start < maxShow) start = Math.max(0, end - maxShow);
  let h = `<span>Showing ${(page * perPage + 1).toLocaleString()}–${Math.min((page + 1) * perPage, total).toLocaleString()} of ${total.toLocaleString()}</span><div class="pg-btns">`;
  h += `<button ${page === 0 ? 'disabled' : ''} onclick="${fn}(false,0)">‹‹</button>`;
  h += `<button ${page === 0 ? 'disabled' : ''} onclick="${fn}(false,${page - 1})">‹</button>`;
  for (let i = start; i < end; i++) { h += `<button class="${i === page ? 'cur' : ''}" onclick="${fn}(false,${i})">${i + 1}</button>` }
  h += `<button ${page >= pages - 1 ? 'disabled' : ''} onclick="${fn}(false,${page + 1})">›</button>`;
  h += `<button ${page >= pages - 1 ? 'disabled' : ''} onclick="${fn}(false,${pages - 1})">››</button></div>`;
  document.getElementById(id).innerHTML = h
}

// ============================================================================
// DASHBOARD + METRICS
// ============================================================================
async function loadDash() {
  skelDash();
  try {
    const d = await api('/dashboard');
    // Dynamic greeting
    const hr = new Date().getHours();
    const greet = hr < 12 ? '☀️ Good Morning' : hr < 17 ? '🌤️ Good Afternoon' : '🌙 Good Evening';
    document.getElementById('dashGreet').textContent = greet;
    const dateStr = new Date().toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
    document.getElementById('dashSub').textContent = dateStr + ' · ' + fNum(d.totalMessages) + ' total emails processed';
    document.getElementById('statsG').innerHTML = `
      <div class="sc"><div class="lb">Active Domains</div><div class="vl cac">${fNum(d.totalDomains)}</div></div>
      <div class="sc"><div class="lb">Active Mailboxes</div><div class="vl cgn">${fNum(d.totalMailboxes)}</div></div>
      <div class="sc"><div class="lb">Total Messages</div><div class="vl cbl">${fNum(d.totalMessages)}</div></div>
      <div class="sc"><div class="lb">Spam Blocked</div><div class="vl crd">${fNum(d.totalSpamBlocked)}</div></div>
      <div class="sc"><div class="lb">Messages Today</div><div class="vl cgn">${fNum(d.messagesToday)}</div></div>
      <div class="sc"><div class="lb">Redis Active</div><div class="vl cac">${fNum(d.redisActiveMailboxes)}</div></div>`;
    const s = d.services || {};
    const slist = [
      { k: 'database', n: 'Database (PG)', ic: '💾' },
      { k: 'redis', n: 'Redis Cache', ic: '⚡' },
      { k: 'rspamd', n: 'Rspamd Filter', ic: '🛡️' },
      { k: 'worker', n: 'Worker Jobs', ic: '⚙️' },
      { k: 'mailserver', n: 'Mailserver Edge', ic: '📮' }
    ];
    document.getElementById('sysSt').innerHTML = slist.map(sv => {
      const info = s[sv.k] || {};
      const on = (typeof info === 'string' ? info === 'ONLINE' : info.status === 'ONLINE');
      const latency = info.latency || '';
      const detail = info.detail || '';
      return `<div class="st-bdg ${on ? 'st-on' : 'st-off'}">
        <div class="dot"></div>
        <div style="flex:1">
          <div>${sv.ic} ${sv.n}</div>
          ${latency ? `<div style="font-size:.7rem;color:var(--tx2);margin-top:2px">⏱ ${latency}</div>` : ''}
          ${detail ? `<div style="font-size:.68rem;color:var(--tx2);font-family:'JetBrains Mono',monospace">${esc(detail)}</div>` : ''}
        </div>
      </div>`
    }).join('');
    // Runtime info panel
    const rt = d.runtime || {};
    if (rt.uptimeStr) {
      document.getElementById('sysSt').innerHTML += `
        <div class="st-bdg st-on" style="min-width:220px">
          <div style="flex:1">
            <div>🖥️ Runtime: ${esc(rt.goVersion)}</div>
            <div style="font-size:.7rem;color:var(--tx2);margin-top:2px">⏱ Uptime: ${esc(rt.uptimeStr)} · CPUs: ${rt.cpus}</div>
            <div style="font-size:.68rem;color:var(--tx2);font-family:'JetBrains Mono',monospace">mem:${rt.allocMB}MB sys:${rt.sysMB}MB goroutines:${rt.goroutines} gc:${rt.gcCycles}</div>
          </div>
        </div>`;
    }
  } catch (e) { }
  loadMetrics();
}

async function loadMetrics() {
  try {
    const m = await api('/metrics');
    const t = m.throughput || {}, s = m.storage || {}, mb = m.mailboxes || {}, sp = m.spam || {};
    document.getElementById('metricsBody').innerHTML = `
      <div class="sg">
        <div class="sc"><div class="lb">Mail / Hour</div><div class="vl cac">${fNum(t.lastHour)}</div></div>
        <div class="sc"><div class="lb">Mail / 24h</div><div class="vl cgn">${fNum(t.last24h)}</div></div>
        <div class="sc"><div class="lb">Total Messages</div><div class="vl cbl">${fNum(s.totalMessages)}</div></div>
        <div class="sc"><div class="lb">Attachments</div><div class="vl cbl">${fNum(s.totalAttachments)}</div></div>
        <div class="sc"><div class="lb">Active Mailboxes</div><div class="vl cgn">${fNum(mb.active)}</div></div>
        <div class="sc"><div class="lb">Expired Pending</div><div class="vl crd">${fNum(mb.expiredPending)}</div></div>
        <div class="sc"><div class="lb">Blocked (Spam)</div><div class="vl crd">${fNum(sp.blockedMessages)}</div></div>
        <div class="sc"><div class="lb">Block Rules</div><div class="vl cac">${fNum(sp.blocklistRules)}</div></div>
      </div>`;
  } catch (e) { document.getElementById('metricsBody').innerHTML = '<p style="color:var(--tx2)">Failed to load metrics</p>' }
}

// ============================================================================
// DOMAINS
// ============================================================================
async function loadDom(reset, pg) {
  if (reset) domPage = 0; if (pg !== undefined) domPage = pg;
  const q = document.getElementById('domQ')?.value || '';
  const st = document.getElementById('domSt')?.value || '';
  ldg('domT');
  try {
    const d = await api(`/domains?search=${encodeURIComponent(q)}&status=${st}&limit=${PER_PAGE}&offset=${domPage * PER_PAGE}`);
    const list = d.domains || []; const total = d.count || 0;
    if (document.getElementById('domCnt')) document.getElementById('domCnt').textContent = fNum(total) + ' domains';
    if (!list.length) { empty('domT', q ? 'No matching domains' : 'No domains yet'); document.getElementById('domPg').innerHTML = ''; return }
    document.getElementById('domT').innerHTML = list.map(x => {
      const nodeName = x.node ? `<span class="badge b-bl">${esc(x.node.name)}</span><br><span style="font-size:.72rem;color:var(--tx2)">${esc(x.node.ipAddress)}</span>` : '<span style="color:var(--tx2)">—</span>';
      return `<tr>
      <td><strong>${esc(x.domainName)}</strong></td>
      <td>${nodeName}</td>
      <td><span class="badge ${x.status === 'ACTIVE' ? 'b-gn' : 'b-rd'}">${x.status}</span></td>
      <td>${x.tenantId ? 'Custom' : 'Public'}</td>
      <td>${fDate(x.createdAt)}</td>
      <td><div class="act">
        <button class="btn btn-i" onclick="checkDNS('${esc(x.domainName)}')">🌐 DNS</button>
        <button class="btn btn-s" onclick="editDom('${x.id}','${x.nodeId||''}','${x.status}','${esc(x.domainName)}')">✏️ Edit</button>
        ${x.status === 'ACTIVE' ? `<button class="btn btn-p" onclick="quickCreateForDomain('${x.id}','${esc(x.domainName)}')">⚡ Mail</button>` : ''}
        <button class="btn btn-d" onclick="delDom('${x.id}','${esc(x.domainName)}')">🗑 Delete</button>
      </div></td></tr>`
    }).join('');
    pgUI('domPg', domPage, total, PER_PAGE, 'loadDom');
  } catch (e) { }
}

async function openAddDomModal() {
  document.getElementById('newDomIn').value = '';
  document.getElementById('addDomResult').innerHTML = '';
  const sel = document.getElementById('newDomNode');
  sel.innerHTML = '<option value="">— No node (manual DNS) —</option>';
  try {
    const d = await api('/nodes');
    (d.nodes || []).forEach(n => {
      sel.innerHTML += `<option value="${n.id}">${esc(n.name)} (${esc(n.ipAddress)}${n.region ? ' / ' + esc(n.region) : ''})</option>`;
    });
  } catch (e) { }
  openModal('addDomM');
}

async function addDom() {
  const n = document.getElementById('newDomIn').value.trim(); if (!n) return;
  const nodeId = document.getElementById('newDomNode').value || null;
  const body = { domainName: n };
  if (nodeId) body.nodeId = nodeId;
  try {
    const r = await fetch(BASE + '/admin/domains', {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const result = await r.json();
    if (!r.ok) { toast(result.error?.message || result.error || 'Failed to add domain', 'e'); return }
    toast(result.reactivated ? 'Domain re-activated!' : 'Domain added');
    const dns = result.dns || [];
    if (dns.length > 0) {
      let h = '<div style="margin-top:.8rem"><strong style="font-size:.82rem">DNS Setup Required:</strong><div class="dns-grid" style="margin-top:.4rem">';
      for (const rec of dns) {
        h += `<div class="dns-row">
          <span class="dns-type">${esc(rec.type)}</span>
          <span class="dns-name">${esc(rec.name)}</span>
          <span class="dns-val">${esc(rec.value)}</span>
          <span class="dns-st dns-warn">${rec.proxy === false ? '☁️ Proxy OFF' : ''}</span>
        </div>`;
      }
      h += '</div><p style="font-size:.75rem;color:var(--tx2);margin-top:.5rem">⚠ Set Cloudflare proxy to OFF (DNS only / grey cloud) for mail records</p></div>';
      document.getElementById('addDomResult').innerHTML = h;
    } else { closeModal() }
    loadDom();
  } catch (e) { toast('Failed to add domain', 'e') }
}

async function delDom(id, name) {
  if (!confirm(`Permanently delete domain "${name}" and ALL its mailboxes/messages?`)) return;
  try { await api('/domains/' + id, 'DELETE'); toast('Domain permanently deleted'); loadDom() } catch (e) { }
}

async function checkDNS(domain) {
  openModal('dnsM');
  document.getElementById('dnsTitle').textContent = 'DNS: ' + domain;
  document.getElementById('dnsBody').innerHTML = '<div class="ldg"><div class="spin"></div>Checking...</div>';
  try {
    const d = await api('/domains/dns-check?domain=' + encodeURIComponent(domain));
    let h = '<div class="dns-grid">';
    for (const r of (d.records || [])) {
      const stCls = r.status === 'OK' ? 'dns-ok' : r.status === 'WARN' ? 'dns-warn' : 'dns-err';
      h += `<div class="dns-row"><span class="dns-type">${esc(r.type)}</span><span class="dns-name">${esc(r.name)}</span><span class="dns-val">${esc(r.value || '—')}</span><span class="dns-st ${stCls}">${r.status}</span></div>`;
    }
    h += '</div>';
    if (d.summary) { h += `<div style="margin-top:.8rem;padding:.6rem;border-radius:8px;font-size:.82rem" class="${d.allOk ? 'dns-ok' : 'dns-warn'}">${esc(d.summary)}</div>` }
    document.getElementById('dnsBody').innerHTML = h;
  } catch (e) { document.getElementById('dnsBody').innerHTML = '<p>Failed</p>' }
}

// ============================================================================
// NODES
// ============================================================================
async function loadNodes(reset, pg) {
  if (reset) nodePage = 0; if (pg !== undefined) nodePage = pg;
  const q = document.getElementById('nodeQ')?.value || '';
  const st = document.getElementById('nodeSt')?.value || '';
  ldg('nodeT');
  try {
    const d = await api(`/nodes?search=${encodeURIComponent(q)}&status=${st}&limit=${PER_PAGE}&offset=${nodePage * PER_PAGE}`);
    const list = d.nodes || []; const total = d.count || 0;
    if (document.getElementById('nodeCnt')) document.getElementById('nodeCnt').textContent = fNum(total) + ' nodes';
    if (!list.length) { empty('nodeT', q ? 'No matching nodes' : 'No nodes yet'); document.getElementById('nodePg').innerHTML = ''; return }
    document.getElementById('nodeT').innerHTML = list.map(x => `<tr>
      <td><strong>${esc(x.name)}</strong></td>
      <td style="font-family:'JetBrains Mono',monospace;font-size:.82rem">${esc(x.ipAddress)}</td>
      <td>${esc(x.region || '—')}</td>
      <td><span class="badge b-bl">${(x.domains || []).length}</span></td>
      <td><span class="badge ${x.status === 'ACTIVE' ? 'b-gn' : 'b-rd'}">${x.status}</span></td>
      <td><div class="act">
        <button class="btn btn-s" onclick="editNode('${x.id}','${esc(x.name)}','${esc(x.ipAddress)}','${esc(x.region||'')}')">Edit</button>
        <button class="btn btn-d" onclick="delNode('${x.id}','${esc(x.name)}')">Delete</button>
      </div></td></tr>`).join('');
    pgUI('nodePg', nodePage, total, PER_PAGE, 'loadNodes');
  } catch (e) { }
}

async function addNode() {
  const name = document.getElementById('newNodeName').value.trim();
  const ip = document.getElementById('newNodeIP').value.trim();
  const region = document.getElementById('newNodeRegion').value.trim();
  if (!name || !ip) { toast('Name and IP are required', 'e'); return }
  try {
    await api('/nodes', 'POST', { name, ipAddress: ip, region });
    closeModal(); toast('Node added'); loadNodes();
  } catch (e) { toast('Failed to add node', 'e') }
}

async function delNode(id, name) {
  if (!confirm(`Delete node "${name}"?`)) return;
  try { await api('/nodes/' + id, 'DELETE'); toast('Node deleted'); loadNodes() }
  catch (e) { toast('Cannot delete: node has domains assigned', 'e') }
}

// ============================================================================
// DOMAIN FILTERS
// ============================================================================
async function loadFilters(reset, pg) {
  if (reset) filterPage = 0; if (pg !== undefined) filterPage = pg;
  const q = document.getElementById('filterQ')?.value || '';
  const ft = document.getElementById('filterType')?.value || '';
  ldg('filterT');
  try {
    const d = await api(`/filters?search=${encodeURIComponent(q)}&type=${ft}&limit=${PER_PAGE}&offset=${filterPage * PER_PAGE}`);
    const list = d.filters || []; const total = d.count || 0;
    if (document.getElementById('filterCnt')) document.getElementById('filterCnt').textContent = fNum(total) + ' filters';
    if (!list.length) { empty('filterT', q ? 'No matching filters' : 'No domain filters yet'); document.getElementById('filterPg').innerHTML = ''; return }
    document.getElementById('filterT').innerHTML = list.map(x => `<tr>
      <td style="font-family:'JetBrains Mono',monospace"><strong>${esc(x.pattern)}</strong></td>
      <td><span class="badge ${x.filterType === 'BLOCK' ? 'b-rd' : 'b-gn'}">${x.filterType}</span></td>
      <td>${esc(x.reason || '—')}</td>
      <td>${fDate(x.createdAt)}</td>
      <td><div class="act">
        <button class="btn btn-s" onclick="editFilter('${x.id}','${esc(x.pattern)}','${x.filterType}','${esc(x.reason||'')}')">✏️ Edit</button>
        <button class="btn btn-d" onclick="delFilter('${x.id}')">🗑 Delete</button>
      </div></td></tr>`).join('');
    pgUI('filterPg', filterPage, total, PER_PAGE, 'loadFilters');
  } catch (e) { }
}

async function addFilter() {
  const pattern = document.getElementById('newFilterPat').value.trim();
  const filterType = document.getElementById('newFilterType').value;
  const reason = document.getElementById('newFilterReason').value.trim();
  if (!pattern) { toast('Pattern is required', 'e'); return }
  try {
    await api('/filters', 'POST', { pattern, filterType, reason });
    closeModal(); toast('Filter added'); loadFilters();
  } catch (e) { toast('Failed: pattern may already exist', 'e') }
}

async function delFilter(id) {
  if (!confirm('Delete this filter?')) return;
  try { await api('/filters/' + id, 'DELETE'); toast('Filter deleted'); loadFilters() } catch (e) { }
}

// ============================================================================
// MAILBOXES
// ============================================================================
async function loadMbox(reset, pg) {
  if (reset) mboxPage = 0; if (pg !== undefined) mboxPage = pg;
  const q = document.getElementById('mboxQ').value;
  const st = document.getElementById('mboxSt').value;
  ldg('mboxT');
  try {
    const d = await api(`/mailboxes?search=${encodeURIComponent(q)}&status=${st}&limit=${PER_PAGE}&offset=${mboxPage * PER_PAGE}`);
    const list = d.mailboxes || []; const total = d.total || 0;
    document.getElementById('mboxCnt').textContent = fNum(total) + ' mailboxes';
    if (!list.length) { empty('mboxT', 'No mailboxes found'); document.getElementById('mboxPg').innerHTML = ''; return }
    document.getElementById('mboxT').innerHTML = list.map(x => {
      const addr = esc(x.localPart) + '@' + (x.domain ? esc(x.domain.domainName) : '?');
      return `<tr>
        <td><strong>${addr}</strong></td>
        <td><span class="badge ${x.status === 'ACTIVE' ? 'b-gn' : x.status === 'EXPIRED' ? 'b-yw' : 'b-rd'}">${x.status}</span></td>
        <td>${esc(x.tenantId || '—')}</td>
        <td>${fTime(x.expiresAt)}</td>
        <td><div class="act">${x.status === 'ACTIVE' ? `<button class="btn btn-d" onclick="delMbox('${x.id}')">🗑 Delete</button>` : ''}</div></td></tr>`
    }).join('');
    pgUI('mboxPg', mboxPage, total, PER_PAGE, 'loadMbox')
  } catch (e) { }
}

async function delMbox(id) {
  if (!confirm('Delete this mailbox?')) return;
  try { await api('/mailboxes/' + id, 'DELETE'); toast('Mailbox deleted'); loadMbox() } catch (e) { }
}

// ============================================================================
// MESSAGES + PREVIEW
// ============================================================================
// ── Messages: selection state ──
const _msgSel = new Set();

function fCountdown(dt) {
  if (!dt) return '—';
  const diff = new Date(dt) - Date.now();
  if (diff <= 0) return '<span style="color:var(--rd)">expired</span>';
  const h = Math.floor(diff / 3600000), m = Math.floor((diff % 3600000) / 60000);
  if (h > 24) { const d = Math.floor(h / 24); return `${d}d ${h % 24}h`; }
  return `${h}h ${m}m`;
}

function toggleMsgSelAll(checked) {
  document.querySelectorAll('.msg-chk').forEach(cb => {
    cb.checked = checked;
    if (checked) _msgSel.add(cb.value); else _msgSel.delete(cb.value);
  });
  updateMsgSelUI();
}

function updateMsgSelUI() {
  const n = _msgSel.size;
  const btn = document.getElementById('msgBulkBtn');
  const cnt = document.getElementById('msgSelCnt');
  btn.style.display = n > 0 ? '' : 'none';
  cnt.textContent = n;
  // Sync header checkbox
  const all = document.querySelectorAll('.msg-chk');
  const hdr = document.getElementById('msgSelAll');
  if (hdr && all.length) hdr.checked = [...all].every(c => c.checked);
}

async function loadMsg(reset, pg, silent) {
  if (reset) { msgPage = 0; _msgSel.clear(); updateMsgSelUI(); }
  if (pg !== undefined) msgPage = pg;
  const q = document.getElementById('msgQ').value;
  const st = document.getElementById('msgSt').value;
  if (!silent) ldg('msgT');
  try {
    let url = `/messages?search=${encodeURIComponent(q)}&limit=${PER_PAGE}&offset=${msgPage * PER_PAGE}`;
    if (st) url += `&mailbox_status=${st}`;
    const d = await api(url);
    const list = d.messages || []; const total = d.total || 0;
    document.getElementById('msgCnt').textContent = fNum(total) + ' messages';
    if (!list.length) {
      empty('msgT', 'No messages found');
      document.getElementById('msgPg').innerHTML = '';
      document.getElementById('msgSelAll').checked = false;
      return;
    }
    document.getElementById('msgT').innerHTML = list.map(x => {
      const spam = x.spamScore || 0;
      const mAddr = x.mailboxAddress || '';
      const mSt = x.mailboxStatus || 'ORPHANED';
      const stBadge = mSt === 'ACTIVE' ? 'b-gn' : mSt === 'EXPIRED' ? 'b-yw' : 'b-rd';
      const checked = _msgSel.has(x.id) ? 'checked' : '';
      return `<tr>
        <td><input type="checkbox" class="msg-chk" value="${x.id}" ${checked} onchange="this.checked?_msgSel.add('${x.id}'):_msgSel.delete('${x.id}');updateMsgSelUI()"></td>
        <td>${esc(x.fromAddress || '')}</td>
        <td>${esc(decodeMIME(x.subject) || '(no subject)')}</td>
        <td>${mAddr ? esc(mAddr) : '<span style="opacity:.5">—</span>'}</td>
        <td><span class="badge ${stBadge}">${mSt}</span></td>
        <td><span class="badge ${spam > 5 ? 'b-rd' : spam > 1 ? 'b-yw' : 'b-gn'}">${spam.toFixed(1)}</span></td>
        <td>${fCountdown(x.expiresAt)}</td>
        <td>${fTime(x.receivedAt)}</td>
        <td><div class="act">
          <button class="btn btn-s" onclick="viewMsg('${x.id}')">👁</button>
          <button class="btn btn-d" onclick="delMsg('${x.id}')">🗑</button>
        </div></td></tr>`;
    }).join('');
    pgUI('msgPg', msgPage, total, PER_PAGE, 'loadMsg');
    updateMsgSelUI();
  } catch (e) { }
}

// ── SSE-based real-time message updates (no polling!) ──
let _sseSource = null;
let _sseActiveTab = '';

function startSSE() {
  if (_sseSource) return;
  if (!TOKEN) return;
  try {
    _sseSource = new EventSource(BASE + '/admin/events?token=' + encodeURIComponent(TOKEN));
    _sseSource.addEventListener('new_message', function(e) {
      // Auto-refresh messages tab if it's active
      if (_sseActiveTab === 'msg') {
        loadMsg(false, undefined, true);  // silent=true → no skeleton flicker
        toast('📨 New email received', 's');
      }
    });
    _sseSource.onerror = function() {
      // Reconnect after 5s on error
      stopSSE();
      setTimeout(startSSE, 5000);
    };
  } catch(e) { }
}

function stopSSE() {
  if (_sseSource) { _sseSource.close(); _sseSource = null; }
}

let _readerMsg = null;

async function viewMsg(id) {
  const panel = document.getElementById('readerPanel');
  const overlay = document.getElementById('readerOverlay');
  overlay.classList.add('on');
  panel.classList.add('on');
  document.getElementById('readerBody').innerHTML = '<div class="ldg"><div class="spin"></div>Loading...</div>';
  document.getElementById('readerAtt').innerHTML = '';
  document.getElementById('readerMeta').innerHTML = '';
  document.getElementById('readerSubject').textContent = 'Loading...';
  try {
    const m = await api('/messages/' + id);
    _readerMsg = m;
    document.getElementById('readerSubject').textContent = decodeMIME(m.subject) || '(no subject)';
    const spam = (m.spamScore || 0).toFixed(1);
    const act = m.quarantineAction || 'ACCEPT';
    document.getElementById('readerMeta').innerHTML = `
      <strong>From</strong><span>${esc(m.fromAddress)}</span>
      <strong>To</strong><span>${esc(m.toAddress)}</span>
      <strong>Subject</strong><span>${esc(decodeMIME(m.subject) || '(no subject)')}</span>
      <strong>Received</strong><span>${fTime(m.receivedAt)}</span>
      <strong>Spam</strong><span><span class="badge ${spam > 5 ? 'b-rd' : spam > 1 ? 'b-yw' : 'b-gn'}">${spam}</span> <span class="badge ${act === 'ACCEPT' ? 'b-gn' : 'b-yw'}">${act}</span></span>`;

    // Render default tab — wrapped in own try so attachments always render
    try {
      readerTab(m.htmlBody ? 'html' : 'text');
    } catch (tabErr) {
      document.getElementById('readerBody').innerHTML = '<div class="reader-empty">Failed to render body — try Text tab</div>';
    }

    // Attachments — download only, no inline preview
    if (m.attachments && m.attachments.length > 0) {
      let ah = `<h4>📎 Attachments (${m.attachments.length})</h4>`;
      for (const a of m.attachments) {
        const ext = (a.filename || '').split('.').pop().toLowerCase();
        const iconTxt = ext.toUpperCase().slice(0,4) || 'FILE';
        const sizeTxt = a.sizeBytes > 1048576 ? (a.sizeBytes/1048576).toFixed(1)+' MB' : (a.sizeBytes/1024).toFixed(1)+' KB';
        ah += `<div class="att-card">
          <div style="display:flex;align-items:center;gap:.6rem;width:100%">
            <div class="att-icon other">${iconTxt}</div>
            <div class="att-info"><div class="att-name">${esc(a.filename)}</div><div class="att-size">${sizeTxt} — ${esc(a.contentType)}</div></div>
            <div class="att-dl" style="margin-left:auto"><button class="btn btn-i" onclick="downloadAtt('${a.id}','${esc(a.filename)}')">⬇ Download</button></div>
          </div>
        </div>`;
      }
      document.getElementById('readerAtt').innerHTML = ah;
    }
  } catch (e) {
    document.getElementById('readerBody').innerHTML = '<div class="reader-empty">Failed to load message</div>';
  }
}

function readerTab(tab) {
  document.querySelectorAll('.reader-tab').forEach(t => t.classList.remove('on'));
  const body = document.getElementById('readerBody');
  const m = _readerMsg;
  if (!m) return;
  // Auto-decode function for base64 and quoted-printable
  const decodeEmailBody = (str) => {
    if (!str) return str;
    str = str.trim();
    // Detect Base64: strip whitespace, check charset, and ensure result looks like text/html
    const stripped = str.replace(/[\r\n\t ]/g, '');
    const isB64 = stripped.length > 20 && /^[A-Za-z0-9+/]+=*$/.test(stripped);
    if (isB64 && !str.includes('<html')) {
      try {
        // Use TextDecoder for robust UTF-8 decoding (handles Thai, CJK, etc.)
        const binStr = atob(stripped);
        const bytes = Uint8Array.from(binStr, c => c.charCodeAt(0));
        return new TextDecoder('utf-8').decode(bytes);
      } catch (e) { /* fallback to raw */ }
    }
    // Detect Quoted-Printable
    if (str.includes('=') && (/=[A-F0-9]{2}/i.test(str) || /=\r?\n/.test(str))) {
      try {
        let qp = str.replace(/=\r?\n/g, '');
        const bytes = Uint8Array.from(qp.replace(/=([A-F0-9]{2})/gi, (_, h) => String.fromCharCode(parseInt(h, 16))), c => c.charCodeAt(0));
        return new TextDecoder('utf-8').decode(bytes);
      } catch (e) { return str; }
    }
    return str;
  };

  const htmlBodyStr = decodeEmailBody(m.htmlBody);
  const textBodyStr = decodeEmailBody(m.textBody);

  if (tab === 'html') {
    document.getElementById('rtHtml').classList.add('on');
    if (htmlBodyStr) {
      let renderedHtml = htmlBodyStr;
      // Strip cid: references (inline images no longer attached)
      renderedHtml = renderedHtml.replace(/src=["']cid:[^"']+["']/gi, 'src=""');
      // Strip any <script> tags for safety (server already sanitizes via bluemonday)
      renderedHtml = renderedHtml.replace(/<script[\s\S]*?<\/script>/gi, '');
      // Render directly into a styled container — no iframe, no blob, no CSP issues
      body.innerHTML = '<div class="reader-html-content" style="font-family:Sarabun,sans-serif;font-size:14px;line-height:1.6;color:#1a1a2e;padding:1rem;word-break:break-word">' + renderedHtml + '</div>';
      // Constrain images and tables inside
      body.querySelectorAll('.reader-html-content img').forEach(img => { img.style.maxWidth = '100%'; img.style.height = 'auto'; });
      body.querySelectorAll('.reader-html-content table').forEach(t => { t.style.maxWidth = '100%'; });
    } else {
      body.innerHTML = '<div class="reader-empty">No HTML body — switch to Text tab</div>';
    }
  } else if (tab === 'text') {
    document.getElementById('rtText').classList.add('on');
    if (textBodyStr) {
      body.innerHTML = `<pre>${esc(textBodyStr)}</pre>`;
    } else {
      body.innerHTML = '<div class="reader-empty">No text body available</div>';
    }
  } else {
    document.getElementById('rtRaw').classList.add('on');
    const raw = JSON.stringify(m, null, 2);
    body.innerHTML = `<pre>${esc(raw)}</pre>`;
  }
}

function closeReader() {
  document.getElementById('readerPanel').classList.remove('on');
  document.getElementById('readerOverlay').classList.remove('on');
  _readerMsg = null;
}

// Download attachment with auth token — simple blob download, no preview
async function downloadAtt(attId, filename) {
  try {
    const r = await fetch(BASE + '/admin/attachment/' + attId, { headers: { 'Authorization': 'Bearer ' + TOKEN } });
    if (!r.ok) throw new Error('Download failed: ' + r.status);
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = filename || attId;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  } catch (e) { toast('Download failed: ' + e.message, 'e'); }
}

async function dlAtt(attId) {
  try {
    const d = await api('/attachment/' + attId);
    if (d.downloadUrl) window.open(d.downloadUrl, '_blank');
    else toast('Download link not available', true);
  } catch (e) { }
}

document.addEventListener('keydown', e => { if (e.key === 'Escape') closeReader(); });

// ============================================================================
// API KEYS
// ============================================================================
async function loadAPIKeys(reset, pg) {
  if (reset) keyPage = 0; if (pg !== undefined) keyPage = pg;
  const q = document.getElementById('keyQ')?.value || '';
  const st = document.getElementById('keySt')?.value || '';
  ldg('keyT');
  try {
    const d = await api(`/api-keys?search=${encodeURIComponent(q)}&status=${st}&limit=${PER_PAGE}&offset=${keyPage * PER_PAGE}`);
    const list = d.keys || []; const total = d.count || 0;
    if (document.getElementById('keyCnt')) document.getElementById('keyCnt').textContent = fNum(total) + ' keys';
    if (!list.length) { empty('keyT', q ? 'No matching keys' : 'No API keys yet'); document.getElementById('keyPg').innerHTML = ''; return }
    document.getElementById('keyT').innerHTML = list.map(x => `<tr>
      <td><strong>${esc(x.name)}</strong>${x.isInternal ? ' <span class="badge-internal" title="Bypasses rate limiting">⚡ Internal</span>' : ''}</td>
      <td style="font-family:'JetBrains Mono',monospace">${esc(x.keyPrefix)}...</td>
      <td>${esc(x.permissions)}</td>
      <td>${x.isInternal ? '<span style="color:var(--gn);font-weight:600">∞ Unlimited</span>' : x.rateLimit+'/min'}</td>
      <td><span class="badge ${x.status === 'ACTIVE' ? 'b-gn' : 'b-rd'}">${x.status}</span></td>
      <td>${fDate(x.createdAt)}</td>
      <td><div class="act">
        <button class="btn btn-s" onclick="editKey('${x.id}','${esc(x.name)}','${esc(x.permissions)}',${x.rateLimit},'${x.status}',${x.isInternal ? 'true' : 'false'})">Edit</button>
        <button class="btn btn-i" onclick="rollKey('${x.id}','${esc(x.name)}')">Roll</button>
        <button class="btn btn-d" onclick="deleteKey('${x.id}','${esc(x.name)}')">Delete</button>
      </div></td></tr>`).join('');
    pgUI('keyPg', keyPage, total, PER_PAGE, 'loadAPIKeys');
  } catch (e) { }
}

async function addAPIKey() {
  const name = document.getElementById('newKeyName').value.trim();
  const permissions = document.getElementById('newKeyPerms').value;
  const rateLimit = parseInt(document.getElementById('newKeyRate').value) || 100;
  const isInternal = document.getElementById('newKeyInternal')?.checked || false;
  if (!name) { toast('Name is required', 'e'); return }
  try {
    const result = await api('/api-keys', 'POST', { name, permissions, rateLimit, isInternal });
    toast('API Key created');
    document.getElementById('newKeyResult').innerHTML = `
      <div style="margin-top:.8rem;padding:.8rem;background:var(--ywbg);border:1px solid #fde68a;border-radius:8px">
        <strong style="font-size:.82rem;color:var(--yw)">⚠ Save this key now — it cannot be shown again!</strong>
        <div style="margin-top:.4rem;font-family:'JetBrains Mono',monospace;font-size:.78rem;background:#fff;padding:.4rem;border-radius:4px;word-break:break-all;user-select:all">${esc(result.rawKey)}</div>
      </div>`;
    // Hide Create button, show only Close
    const btns = document.querySelector('#addKeyM .modal-btns');
    if (btns) btns.innerHTML = '<button class="btn btn-s" onclick="closeModal()">Close</button>';
    loadAPIKeys();
  } catch (e) { toast(e.message || 'Failed to create key', 'e') }
}

async function deleteKey(id, name) {
  if (!confirm(`Permanently delete API key "${name}"? This cannot be undone.`)) return;
  try { await api('/api-keys/' + id, 'DELETE'); toast('Key deleted'); loadAPIKeys() } catch (e) { toast(e.message || 'Failed to delete key', 'e') }
}

async function rollKey(id, name) {
  if (!confirm(`Roll API key "${name}"? The old key will immediately stop working.`)) return;
  try {
    const result = await api('/api-keys/' + id + '/roll', 'POST');
    toast('API Key rolled successfully');
    document.getElementById('editM').classList.remove('on'); // in case it's open
    
    // We reuse the newKeyResult logic but we must open a modal to show it since this is a table action
    const displayHtml = `
      <div style="margin-top:.8rem;padding:.8rem;background:var(--ywbg);border:1px solid #fde68a;border-radius:8px">
        <strong style="font-size:.82rem;color:var(--yw)">⚠ Save this NEW key now — it cannot be shown again!</strong>
        <div style="margin-top:.4rem;font-family:'JetBrains Mono',monospace;font-size:.78rem;background:#fff;padding:.4rem;border-radius:4px;word-break:break-all;user-select:all">${esc(result.rawKey)}</div>
      </div>`;
    
    // Repurpose addition modal just to show the key
    document.getElementById('newKeyResult').innerHTML = displayHtml;
    document.getElementById('newKeyName').parentElement.style.display = 'none';
    document.getElementById('newKeyPerms').parentElement.style.display = 'none';
    document.getElementById('newKeyRate').parentElement.style.display = 'none';
    
    const btns = document.querySelector('#addKeyM .modal-btns');
    if (btns) btns.innerHTML = '<button class="btn btn-s" onclick="closeModal(); resetKeyModal()">Close</button>';
    
    openModal('addKeyM');
    loadAPIKeys();
  } catch (e) { toast(e.message || 'Failed to roll key', 'e') }
}

function resetKeyModal() {
  document.getElementById('newKeyResult').innerHTML = '';
  document.getElementById('newKeyName').parentElement.style.display = '';
  document.getElementById('newKeyPerms').parentElement.style.display = '';
  document.getElementById('newKeyRate').parentElement.style.display = '';
  const btns = document.querySelector('#addKeyM .modal-btns');
  if (btns) btns.innerHTML = '<button class="btn btn-s" onclick="closeModal()">Cancel</button><button class="btn btn-p" onclick="addAPIKey()">Create</button>';
}

// ============================================================================
// AUDIT LOG — search + dynamic action filter
// ============================================================================
let _auditActionsLoaded = false;
async function loadAudit(reset, pg) {
  if (reset) auditPage = 0; if (pg !== undefined) auditPage = pg;
  ldg('auditT');
  try {
    const q = (document.getElementById('auditQ')?.value || '').trim();
    const act = (document.getElementById('auditAction')?.value || '');
    const d = await api(`/audit-log?limit=${PER_PAGE}&offset=${auditPage * PER_PAGE}&search=${encodeURIComponent(q)}&action=${encodeURIComponent(act)}`);
    const list = d.logs || [];
    const total = d.total || 0;
    document.getElementById('auditCnt').textContent = `${total.toLocaleString()} entries`;

    // Populate action filter dropdown dynamically (once or on reset)
    if (!_auditActionsLoaded || reset) {
      const sel = document.getElementById('auditAction');
      const current = sel.value;
      sel.innerHTML = '<option value="">All Actions</option>';
      (d.actions || []).sort().forEach(a => {
        sel.innerHTML += `<option value="${esc(a)}" ${a === current ? 'selected' : ''}>${esc(a)}</option>`;
      });
      _auditActionsLoaded = true;
    }

    if (!list.length) { empty('auditT', 'No audit logs found'); document.getElementById('auditPg').innerHTML = ''; return }
    document.getElementById('auditT').innerHTML = list.map(x => `<tr>
      <td><span class="badge b-bl">${esc(x.action || '')}</span></td>
      <td style="font-family:'JetBrains Mono',monospace;font-size:.78rem;max-width:300px;overflow:hidden;text-overflow:ellipsis" title="${esc(x.targetId||'')}">${esc(x.targetId || '')}</td>
      <td>${esc(x.userId || 'system')}</td>
      <td style="font-family:'JetBrains Mono',monospace;font-size:.78rem">${esc(x.ipAddress || '—')}</td>
      <td>${fTime(x.createdAt)}</td></tr>`).join('');
    pgUI('auditPg', auditPage, total, PER_PAGE, 'loadAudit');
  } catch (e) { }
}

// ============================================================================
// SETTINGS — Thai descriptions + EXPORT/IMPORT
// ============================================================================
const SETTING_META = {
  spam_reject_threshold:     { label: 'Spam Reject Threshold', desc: 'คะแนนสแปมขั้นต่ำที่จะปฏิเสธอีเมล (ค่ายิ่งต่ำ = เข้มงวดมาก, แนะนำ 10–15)', type: 'number' },
  default_ttl_hours:         { label: 'Default Mailbox TTL', desc: 'อายุถังจดหมายเริ่มต้น (ชั่วโมง) — หลังหมดอายุจะถูกลบอัตโนมัติ', type: 'number' },
  default_message_ttl_hours: { label: 'Default Message TTL', desc: 'อายุข้อความเริ่มต้น (ชั่วโมง) — ข้อความที่เก่ากว่านี้จะถูก worker ลบ', type: 'number' },
  max_message_size_mb:       { label: 'Max Message Size (MB)', desc: 'ขนาดอีเมลสูงสุดที่รับได้ (MB) — อีเมลที่ใหญ่กว่านี้จะถูกปฏิเสธ', type: 'number' },
  max_mailboxes_free:        { label: 'Max Mailboxes (Free)', desc: 'จำนวนถังจดหมายสูงสุดต่อ tenant ฟรี — ป้องกัน abuse', type: 'number' },
  max_attachments:           { label: 'Max Attachments', desc: 'จำนวนไฟล์แนบสูงสุดต่ออีเมล — เกินนี้จะถูกตัดออก', type: 'number' },
  max_attachment_size_mb:    { label: 'Max Attachment Size (MB)', desc: 'ขนาดไฟล์แนบสูงสุดต่อไฟล์ (MB) — ไฟล์ที่ใหญ่กว่าจะถูกข้าม', type: 'number' },
  allow_anonymous:           { label: 'Allow Anonymous', desc: 'อนุญาตสร้างถังจดหมายโดยไม่ต้องมี API key (true/false)', type: 'text' },
  webhook_url:               { label: 'Webhook URL', desc: 'URL ที่จะรับการแจ้งเตือนเมื่อมีอีเมลเข้า — เว้นว่างถ้าไม่ใช้', type: 'text' },
  webhook_secret:            { label: 'Webhook Secret', desc: 'คีย์ลับสำหรับตรวจสอบลายเซ็น webhook (HMAC-SHA256)', type: 'text' },
};

async function loadSet() {
  try {
    const d = await api('/settings'); const s = d.settings || {};
    document.getElementById('setForm').innerHTML = Object.entries(s).map(([k, v]) => {
      const meta = SETTING_META[k] || { label: k.replace(/_/g, ' ').toUpperCase(), desc: '', type: 'text' };
      return `<div class="fg">
        <label>${esc(meta.label)}</label>
        <input type="${meta.type}" data-key="${k}" value="${esc(v)}">
        ${meta.desc ? `<div style="font-size:.72rem;color:var(--tx2);margin-top:2px">${esc(meta.desc)}</div>` : ''}
      </div>`
    }).join('');
  } catch (e) { }
}

async function saveSet() {
  const b = {}; document.querySelectorAll('#setForm input').forEach(el => { b[el.dataset.key] = el.value });
  try { await api('/settings', 'POST', b); toast('Settings saved') } catch (e) { toast(e.message || 'Failed to save settings', 'e') }
}

async function exportConfig() {
  try {
    const r = await fetch(BASE + '/admin/export', { headers: { 'Authorization': 'Bearer ' + TOKEN } });
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a'); a.href = url;
    a.download = `tempmail-config-${new Date().toISOString().split('T')[0]}.json`;
    a.click(); URL.revokeObjectURL(url);
    toast('Config exported');
  } catch (e) { toast('Export failed', 'e') }
}

async function importConfig(input) {
  const file = input.files[0]; if (!file) return;
  const text = await file.text();
  try {
    await api('/import', 'POST', JSON.parse(text));
    toast('Config imported'); loadSet();
  } catch (e) { toast('Import failed', 'e') }
  input.value = '';
}

// ── Modal ──
function openModal(id) { document.getElementById(id).classList.add('on') }
function closeModal() {
  document.querySelectorAll('.modal-bg').forEach(m => m.classList.remove('on'));
  const r = document.getElementById('addDomResult'); if (r) r.innerHTML = '';
  const k = document.getElementById('newKeyResult'); if (k) k.innerHTML = '';
  // Restore Create API Key modal buttons + clear inputs
  const kb = document.querySelector('#addKeyM .modal-btns');
  if (kb) kb.innerHTML = '<button class="btn btn-s" onclick="closeModal()">Cancel</button><button class="btn btn-p" onclick="addAPIKey()">Create</button>';
  const kn = document.getElementById('newKeyName'); if (kn) kn.value = '';
}

// ============================================================================
// UNIVERSAL EDIT MODAL — premium replacement for all prompt() dialogs
// ============================================================================

let _editState = {};

function openEditModal(title, fields, saveFn) {
  _editState = { saveFn };
  document.getElementById('editTitle').textContent = title;
  let h = '';
  for (const f of fields) {
    if (f.type === 'select') {
      h += `<div class="fg"><label>${esc(f.label)}</label><select id="edit_${f.key}">`;
      for (const o of f.options) {
        h += `<option value="${esc(o.value)}" ${o.value === f.value ? 'selected' : ''}>${esc(o.label)}</option>`;
      }
      h += `</select></div>`;
    } else if (f.type === 'toggle') {
      h += `<div class="fg toggle-row">
        <label>${esc(f.label)}</label>
        <div class="toggle-wrap">
          <label class="toggle-switch">
            <input type="checkbox" id="edit_${f.key}" ${f.value ? 'checked' : ''}>
            <span class="toggle-slider"></span>
          </label>
          <span class="toggle-label" id="edit_${f.key}_lbl">${f.value ? f.onLabel || 'On' : f.offLabel || 'Off'}</span>
        </div>
        ${f.desc ? `<div class="toggle-desc">${esc(f.desc)}</div>` : ''}
      </div>`;
    } else {
      h += `<div class="fg"><label>${esc(f.label)}</label><input type="${f.type || 'text'}" id="edit_${f.key}" value="${esc(f.value || '')}"></div>`;
    }
  }
  document.getElementById('editFields').innerHTML = h;
  // Wire toggle label update
  document.querySelectorAll('#editFields .toggle-switch input').forEach(cb => {
    const lbl = document.getElementById(cb.id + '_lbl');
    const field = fields.find(f => 'edit_' + f.key === cb.id);
    if (lbl && field) {
      cb.addEventListener('change', () => {
        lbl.textContent = cb.checked ? (field.onLabel || 'On') : (field.offLabel || 'Off');
        lbl.style.color = cb.checked ? 'var(--gn)' : 'var(--tx2)';
      });
      lbl.style.color = cb.checked ? 'var(--gn)' : 'var(--tx2)';
    }
  });
  openModal('editM');
}

function getEditVal(key) { return document.getElementById('edit_' + key)?.value || '' }

async function saveEdit() {
  if (_editState.saveFn) {
    const btn = document.getElementById('editSaveBtn');
    btn.disabled = true; btn.textContent = 'Saving...';
    try { await _editState.saveFn() }
    finally { btn.disabled = false; btn.textContent = 'Save Changes' }
  }
}

// ── Edit Domain (modal with status + node selector) ──
async function editDom(id, nodeId, status, domainName) {
  let nodeOptions = [{ value: '', label: '— No node —' }];
  try {
    const d = await api('/nodes');
    (d.nodes || []).forEach(n => nodeOptions.push({
      value: n.id, label: `${n.name} (${n.ipAddress})`
    }));
  } catch (e) { }

  openEditModal('Edit Domain: ' + domainName, [
    { key: 'status', label: 'Status', type: 'select', value: status, options: [
      { value: 'ACTIVE', label: '🟢 ACTIVE' },
      { value: 'PENDING', label: '🟡 PENDING' },
      { value: 'DISABLED', label: '🔴 DISABLED' },
    ]},
    { key: 'nodeId', label: 'Assign to Node', type: 'select', value: nodeId || '', options: nodeOptions },
  ], async () => {
    try {
      const nid = getEditVal('nodeId');
      await api('/domains/' + id, 'PUT', { status: getEditVal('status'), nodeId: nid || null });
      toast('Domain updated'); closeModal(); loadDom();
    } catch (e) { toast(e.message || 'Failed to update domain', 'e') }
  });
}

// ── Edit Node (modal with name, IP, region, status) ──
async function editNode(id, name, ip, region, status) {
  openEditModal('Edit Node: ' + name, [
    { key: 'name', label: 'Node Name', value: name },
    { key: 'ipAddress', label: 'IP Address', value: ip },
    { key: 'region', label: 'Region', value: region },
    { key: 'status', label: 'Status', type: 'select', value: status, options: [
      { value: 'ACTIVE', label: '🟢 ACTIVE' },
      { value: 'DISABLED', label: '🔴 DISABLED' },
    ]},
  ], async () => {
    try {
      await api('/nodes/' + id, 'PUT', {
        name: getEditVal('name'), ipAddress: getEditVal('ipAddress'),
        region: getEditVal('region'), status: getEditVal('status')
      });
      toast('Node updated'); closeModal(); loadNodes();
    } catch (e) { toast(e.message || 'Failed to update node', 'e') }
  });
}

// ── Edit Filter (modal with pattern, type, reason) ──
async function editFilter(id, pattern, type, reason) {
  openEditModal('Edit Filter: ' + pattern, [
    { key: 'pattern', label: 'Domain Pattern', value: pattern },
    { key: 'filterType', label: 'Type', type: 'select', value: type, options: [
      { value: 'BLOCK', label: '🚫 BLOCK' },
      { value: 'ALLOW', label: '✅ ALLOW' },
    ]},
    { key: 'reason', label: 'Reason', value: reason },
  ], async () => {
    try {
      await api('/filters/' + id, 'PUT', {
        pattern: getEditVal('pattern'), filterType: getEditVal('filterType'), reason: getEditVal('reason')
      });
      toast('Filter updated'); closeModal(); loadFilters();
    } catch (e) { toast(e.message || 'Failed to update filter', 'e') }
  });
}

// ── Edit API Key (modal with name, permissions, rate limit, status, isInternal toggle) ──
async function editKey(id, name, perms, rate, status, isInternal) {
  openEditModal('Edit API Key: ' + name, [
    { key: 'name', label: 'Key Name', value: name },
    { key: 'permissions', label: 'Permissions', type: 'select', value: perms, options: [
      { value: 'read,write', label: 'Read + Write' },
      { value: 'read', label: 'Read Only' },
    ]},
    { key: 'rateLimit', label: 'Rate Limit (req/min)', type: 'number', value: String(rate) },
    { key: 'status', label: 'Status', type: 'select', value: status, options: [
      { value: 'ACTIVE', label: '🟢 ACTIVE' },
      { value: 'DISABLED', label: '🔴 DISABLED' },
    ]},
    {
      key: 'isInternal', label: '⚡ Internal Mode (Bypass Rate Limit)', type: 'toggle',
      value: isInternal === true || isInternal === 'true',
      onLabel: 'Enabled — No rate limits applied',
      offLabel: 'Disabled — Rate limits apply',
      desc: 'Internal keys bypass all rate limiting. Use only for trusted internal services.',
    },
  ], async () => {
    try {
      const isInternalVal = document.getElementById('edit_isInternal')?.checked || false;
      await api('/api-keys/' + id, 'PUT', {
        name: getEditVal('name'), permissions: getEditVal('permissions'),
        rateLimit: parseInt(getEditVal('rateLimit')) || 100, status: getEditVal('status'),
        isInternal: isInternalVal,
      });
      toast('API Key updated'); closeModal(); loadAPIKeys();
    } catch (e) { toast(e.message || 'Failed to update key', 'e') }
  });
}

async function delMsg(id) {
  if (!confirm('Delete this message permanently?')) return;
  try {
    await api('/messages/' + id, 'DELETE');
    _msgSel.delete(id);
    toast('Message deleted');
    loadMsg();
  } catch (e) { toast(e.message || 'Failed to delete message', 'e') }
}

async function bulkDeleteMsg() {
  const ids = [..._msgSel];
  if (!ids.length) return;
  if (!confirm(`Delete ${ids.length} message(s) permanently? This cannot be undone.`)) return;
  try {
    // Process in chunks of 100
    for (let i = 0; i < ids.length; i += 100) {
      const chunk = ids.slice(i, i + 100);
      await api('/messages/bulk-delete', 'POST', { ids: chunk });
    }
    toast(`${ids.length} message(s) deleted`);
    _msgSel.clear();
    updateMsgSelUI();
    loadMsg(true);
  } catch (e) { toast(e.message || 'Bulk delete failed', 'e') }
}

async function quickCreateMbox() {
  // Fetch domains for selector
  let domains = [];
  try {
    const d = await api('/domains');
    domains = (d.domains || []).filter(x => x.status === 'ACTIVE');
  } catch (e) { }

  if (!domains.length) { toast('No active domains. Add a domain first.', 'e'); return }

  openEditModal('⚡ Quick Create Mailbox', [
    { key: 'domainId', label: 'Domain', type: 'select', value: domains[0].id, options: domains.map(d => ({
      value: d.id, label: d.domainName
    }))},
    { key: 'ttlHours', label: 'TTL (hours)', type: 'number', value: '1' },
  ], async () => {
    try {
      const r = await fetch(BASE + '/admin/mailboxes/quick-create', {
        method: 'POST',
        headers: { 'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json' },
        body: JSON.stringify({ domainId: getEditVal('domainId'), ttlHours: parseInt(getEditVal('ttlHours')) || 1 })
      });
      const result = await r.json();
      if (!r.ok) { toast(result.error?.message || 'Failed to create', 'e'); return }
      toast('Created: ' + result.address);
      closeModal(); loadMbox(true);
    } catch (e) { toast('Failed to create', 'e') }
  });
}

// Quick create mailbox for a specific domain (from domain row)
async function quickCreateForDomain(domainId, domainName) {
  openEditModal('⚡ Quick Create on ' + domainName, [
    { key: 'ttlHours', label: 'TTL (hours)', type: 'number', value: '1' },
  ], async () => {
    try {
      const r = await fetch(BASE + '/admin/mailboxes/quick-create', {
        method: 'POST',
        headers: { 'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json' },
        body: JSON.stringify({ domainId: domainId, ttlHours: parseInt(getEditVal('ttlHours')) || 1 })
      });
      const result = await r.json();
      if (!r.ok) { toast(result.error?.message || 'Failed to create', 'e'); return }
      toast('Created: ' + result.address);
      closeModal();
    } catch (e) { toast('Failed to create', 'e') }
  });
}

async function testWebhook() {
  try {
    const result = await api('/webhook-test', 'POST');
    if (result.status === 'ok') { toast('Webhook OK: ' + (result.response || '').substring(0, 100)) }
    else { toast('Webhook error: ' + (result.error || 'Unknown'), 'e') }
  } catch (e) { toast('Webhook test failed', 'e') }
}

