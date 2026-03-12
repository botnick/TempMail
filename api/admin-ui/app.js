// ============================================================================
// TempMail Admin — Application Logic
// ============================================================================

let TOKEN = '';
let USERNAME = '';
const BASE = location.origin;
const SK = 'tm_admin_token';
const UK = 'tm_admin_user';
const PER_PAGE = 30;
let mboxPage = 0, msgPage = 0, auditPage = 0;
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

// Init — check existing session
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
      hideErr(); showApp(); loadDash()
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
function logout() { TOKEN = ''; USERNAME = ''; clearSession(); showLogin() }
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
  if (r.status === 401 || r.status === 403) { logout(); toast('Session expired', 'e'); throw new Error('401') }
  return r.json()
}

// ── UI Utilities ──
function toast(m, t = 's') { const d = document.createElement('div'); d.className = 'toast toast-' + t; d.textContent = m; document.body.appendChild(d); setTimeout(() => d.remove(), 3000) }
function dSearch(fn) { clearTimeout(_dt); _dt = setTimeout(() => { fn(true) }, 300) }

function tab(n, b) {
  document.querySelectorAll('.nav button').forEach(t => t.classList.remove('on'));
  document.querySelectorAll('.pn').forEach(p => p.classList.remove('on'));
  document.getElementById('pn-' + n).classList.add('on'); if (b) b.classList.add('on');
  const ld = { dash: loadDash, dom: loadDom, mbox: () => loadMbox(true), msg: () => loadMsg(true), audit: () => loadAudit(true), set: loadSet };
  if (ld[n]) ld[n]()
}

// ── Format Helpers ──
function fDate(s) { if (!s) return '—'; const d = new Date(s); return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }) }
function fTime(s) { if (!s) return '—'; const d = new Date(s); return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }) + ' ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }) }
function fNum(n) { return (n || 0).toLocaleString() }
function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML }

// ── Loading / Empty ──
function ldg(id) { document.getElementById(id).innerHTML = '<tr><td colspan="10"><div class="ldg"><div class="spin"></div>Loading...</div></td></tr>' }
function empty(id, msg) { document.getElementById(id).innerHTML = `<tr><td colspan="10"><div class="empty"><div class="ic">📭</div><p>${msg}</p></div></td></tr>` }

// ── Pagination ──
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
// DASHBOARD
// ============================================================================
async function loadDash() {
  try {
    const d = await api('/dashboard');
    document.getElementById('statsG').innerHTML = `
      <div class="sc"><div class="lb">Active Domains</div><div class="vl cac">${fNum(d.totalDomains)}</div></div>
      <div class="sc"><div class="lb">Active Mailboxes</div><div class="vl cgn">${fNum(d.totalMailboxes)}</div></div>
      <div class="sc"><div class="lb">Total Messages</div><div class="vl cbl">${fNum(d.totalMessages)}</div></div>
      <div class="sc"><div class="lb">Spam Blocked</div><div class="vl crd">${fNum(d.totalSpamBlocked)}</div></div>
      <div class="sc"><div class="lb">Messages Today</div><div class="vl cgn">${fNum(d.messagesToday)}</div></div>
      <div class="sc"><div class="lb">Redis Active</div><div class="vl cac">${fNum(d.redisActiveMailboxes)}</div></div>`;

    // System Status
    const s = d.services || {};
    const slist = [
      { k: 'database', n: 'Database (PG)' },
      { k: 'redis', n: 'Redis Cache' },
      { k: 'rspamd', n: 'Rspamd Filter' },
      { k: 'worker', n: 'Worker Jobs' },
      { k: 'mailserver', n: 'Mailserver Edge' }
    ];
    document.getElementById('sysSt').innerHTML = slist.map(sv => {
      const on = s[sv.k] === 'ONLINE';
      return `<div class="st-bdg ${on ? 'st-on' : 'st-off'}"><div class="dot"></div>${sv.n}</div>`
    }).join('');
  } catch (e) { }
}

// ============================================================================
// DOMAINS
// ============================================================================
async function loadDom() {
  ldg('domT');
  try {
    const d = await api('/domains'); const list = d.domains || [];
    if (!list.length) { empty('domT', 'No domains yet'); return }
    document.getElementById('domT').innerHTML = list.map(x => `<tr>
      <td><strong>${esc(x.domainName)}</strong></td>
      <td><span class="badge ${x.status === 'ACTIVE' ? 'b-gn' : 'b-rd'}">${x.status}</span></td>
      <td>${x.tenantId ? 'Custom' : 'Public'}</td>
      <td>${fDate(x.createdAt)}</td>
      <td>
        <button class="btn btn-i" onclick="checkDNS('${esc(x.domainName)}')">DNS Check</button>
        <button class="btn btn-d" onclick="delDom('${x.id}','${esc(x.domainName)}')">Delete</button>
      </td></tr>`).join('')
  } catch (e) { }
}

async function addDom() {
  const n = document.getElementById('newDomIn').value.trim(); if (!n) return;
  try { await api('/domains', 'POST', { domainName: n }); closeModal(); document.getElementById('newDomIn').value = ''; toast('Domain added'); loadDom() }
  catch (e) { toast('Failed to add domain', 'e') }
}

async function delDom(id, name) {
  if (!confirm(`Delete domain "${name}" and all its mailboxes?`)) return;
  try { await api('/domains/' + id, 'DELETE'); toast('Domain deleted'); loadDom() } catch (e) { }
}

// ── DNS Check ──
async function checkDNS(domain) {
  openModal('dnsM');
  document.getElementById('dnsTitle').textContent = 'DNS Check: ' + domain;
  document.getElementById('dnsBody').innerHTML = '<div class="ldg"><div class="spin"></div>Checking DNS records...</div>';

  try {
    const d = await api('/domains/dns-check?domain=' + encodeURIComponent(domain));
    const records = d.records || [];
    let h = '<div class="dns-grid">';
    for (const r of records) {
      const stCls = r.status === 'OK' ? 'dns-ok' : r.status === 'WARN' ? 'dns-warn' : 'dns-err';
      const stIcon = r.status === 'OK' ? '✓' : r.status === 'WARN' ? '⚠' : '✗';
      h += `<div class="dns-row">
        <span class="dns-type">${esc(r.type)}</span>
        <span class="dns-name">${esc(r.name)}</span>
        <span class="dns-val">${esc(r.value || '—')}</span>
        <span class="dns-st ${stCls}">${stIcon} ${esc(r.status)}</span>
      </div>`;
    }
    h += '</div>';
    if (d.summary) {
      const sumCls = d.allOk ? 'dns-ok' : 'dns-warn';
      h += `<div style="margin-top:.8rem;padding:.6rem;border-radius:8px;font-size:.82rem" class="${sumCls}">${esc(d.summary)}</div>`;
    }
    document.getElementById('dnsBody').innerHTML = h;
  } catch (e) {
    document.getElementById('dnsBody').innerHTML = '<div class="empty"><p>Failed to check DNS</p></div>';
  }
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
        <td>${x.status === 'ACTIVE' ? `<button class="btn btn-d" onclick="delMbox('${x.id}')">Delete</button>` : ''}</td></tr>`
    }).join('');
    pgUI('mboxPg', mboxPage, total, PER_PAGE, 'loadMbox')
  } catch (e) { }
}

async function delMbox(id) {
  if (!confirm('Delete this mailbox?')) return;
  try { await api('/mailboxes/' + id, 'DELETE'); toast('Mailbox deleted'); loadMbox() } catch (e) { }
}

// ============================================================================
// MESSAGES
// ============================================================================
async function loadMsg(reset, pg) {
  if (reset) msgPage = 0; if (pg !== undefined) msgPage = pg;
  const q = document.getElementById('msgQ').value;
  ldg('msgT');
  try {
    const d = await api(`/messages?search=${encodeURIComponent(q)}&limit=${PER_PAGE}&offset=${msgPage * PER_PAGE}`);
    const list = d.messages || []; const total = d.total || 0;
    document.getElementById('msgCnt').textContent = fNum(total) + ' messages';
    if (!list.length) { empty('msgT', 'No messages found'); document.getElementById('msgPg').innerHTML = ''; return }
    document.getElementById('msgT').innerHTML = list.map(x => {
      const spam = x.spamScore || 0; const act = x.quarantineAction || 'ACCEPT';
      return `<tr>
        <td>${esc(x.fromAddress || '')}</td>
        <td>${esc(x.subject || '(no subject)')}</td>
        <td><span class="badge ${spam > 5 ? 'b-rd' : spam > 1 ? 'b-yw' : 'b-gn'}">${spam.toFixed(1)}</span></td>
        <td><span class="badge ${act === 'ACCEPT' ? 'b-gn' : 'b-yw'}">${act}</span></td>
        <td>${fTime(x.receivedAt)}</td></tr>`
    }).join('');
    pgUI('msgPg', msgPage, total, PER_PAGE, 'loadMsg')
  } catch (e) { }
}

// ============================================================================
// AUDIT LOG
// ============================================================================
async function loadAudit(reset, pg) {
  if (reset) auditPage = 0; if (pg !== undefined) auditPage = pg;
  ldg('auditT');
  try {
    const d = await api(`/audit-log?limit=${PER_PAGE}&offset=${auditPage * PER_PAGE}`);
    const list = d.logs || [];
    if (!list.length) { empty('auditT', 'No audit logs'); document.getElementById('auditPg').innerHTML = ''; return }
    document.getElementById('auditT').innerHTML = list.map(x => `<tr>
      <td><span class="badge b-bl">${esc(x.action || '')}</span></td>
      <td style="font-family:'JetBrains Mono',monospace;font-size:.78rem">${esc(x.targetId || '')}</td>
      <td>${esc(x.userId || 'system')}</td>
      <td>${esc(x.ipAddress || '—')}</td>
      <td>${fTime(x.createdAt)}</td></tr>`).join('');
    const total = d.count || list.length;
    if (total >= PER_PAGE) pgUI('auditPg', auditPage, Math.max(total, (auditPage + 2) * PER_PAGE), PER_PAGE, 'loadAudit')
  } catch (e) { }
}

// ============================================================================
// SETTINGS
// ============================================================================
async function loadSet() {
  try {
    const d = await api('/settings'); const s = d.settings || {};
    document.getElementById('setForm').innerHTML = Object.entries(s).map(([k, v]) => `<div class="fg">
      <label>${k.replace(/_/g, ' ').toUpperCase()}</label>
      <input type="text" data-key="${k}" value="${esc(v)}"></div>`).join('')
  } catch (e) { }
}

async function saveSet() {
  const b = {}; document.querySelectorAll('#setForm input').forEach(el => { b[el.dataset.key] = el.value });
  try { await api('/settings', 'POST', b); toast('Settings saved') } catch (e) { toast('Failed to save', 'e') }
}

// ── Modal ──
function openModal(id) { document.getElementById(id).classList.add('on') }
function closeModal() { document.querySelectorAll('.modal-bg').forEach(m => m.classList.remove('on')) }
