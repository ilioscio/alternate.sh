// alternate.sh web client: login/signup, the xterm.js terminal, and the
// WebSocket relay. The terminal WS carries binary frames (terminal bytes)
// and text frames (JSON control: resize upstream, call signaling downstream).
'use strict';

import { xtermThemes } from './themes.js';

// ── State ────────────────────────────────────────────────────────────────────
let currentTheme = localStorage.getItem('as-theme') || 'white-black';
let crtEnabled   = localStorage.getItem('as-crt') !== '0';
let authToken    = localStorage.getItem('as-token');
let authUser     = localStorage.getItem('as-user');

let term    = null;
let fitAddon = null;
let activeWS = null;

// Control-message handlers, keyed by message type. The call UI registers
// handlers here; unknown types are ignored.
const controlHandlers = new Map();

export function onControl(type, fn) {
  controlHandlers.set(type, fn);
}

export function getTheme() {
  return currentTheme;
}

export function getToken() {
  return authToken;
}

// themeListeners lets the call panel re-tint live when the theme changes.
const themeListeners = [];
export function onThemeChange(fn) {
  themeListeners.push(fn);
}

// ── DOM refs ─────────────────────────────────────────────────────────────────
const loginScreen     = document.getElementById('login-screen');
const loginForm       = document.getElementById('login-form');
const inpUser         = document.getElementById('inp-user');
const inpPass         = document.getElementById('inp-pass');
const loginError      = document.getElementById('login-error');
const loginBtn        = document.getElementById('login-btn');
const loginCrt        = document.getElementById('login-crt');
const loginThemeRadios = document.getElementById('login-theme-radios');

const termScreen  = document.getElementById('terminal-screen');
const sbUser      = document.getElementById('sb-user');
const sbTheme     = document.getElementById('sb-theme');
const sbCrt       = document.getElementById('sb-crt');
const sbDisconnect = document.getElementById('sb-disconnect');
const termEl      = document.getElementById('terminal');

// ── Theme & CRT ──────────────────────────────────────────────────────────────
function applyTheme(t) {
  const html = document.documentElement;
  html.className = html.className.replace(/\btheme-\S+/g, '');
  html.classList.add('theme-' + t);
  currentTheme = t;
  localStorage.setItem('as-theme', t);
  // Sync controls
  document.querySelectorAll('input[name=theme]').forEach(r => {
    r.checked = (r.value === t);
  });
  sbTheme.value = t;
  // Update live terminal if open
  if (term) term.options.theme = xtermThemes[t];
  for (const fn of themeListeners) fn(t);
}

function applyCRT(on) {
  crtEnabled = on;
  localStorage.setItem('as-crt', on ? '1' : '0');
  document.documentElement.classList.toggle('crt-on', on);
  loginCrt.checked = on;
  sbCrt.checked    = on;
}

// ── Login/logout screens ─────────────────────────────────────────────────────
function showLogin() {
  termScreen.hidden = true;
  loginScreen.hidden = false;
  inpUser.value = '';
  inpPass.value = '';
  loginError.textContent = '';
  loginBtn.disabled = false;
  showForm('login');
  inpUser.focus();
  if (term) { term.dispose(); term = null; }
}

function showTerminal(username) {
  loginScreen.hidden = true;
  termScreen.hidden  = false;
  sbUser.textContent = username;
}

// ── Init controls ────────────────────────────────────────────────────────────
applyTheme(currentTheme);
applyCRT(crtEnabled);

loginThemeRadios.addEventListener('change', e => {
  if (e.target.name === 'theme') applyTheme(e.target.value);
});
loginCrt.addEventListener('change', () => applyCRT(loginCrt.checked));
sbTheme.addEventListener('change', () => applyTheme(sbTheme.value));
sbCrt.addEventListener('change',   () => applyCRT(sbCrt.checked));

// ── Login flow ───────────────────────────────────────────────────────────────
loginForm.addEventListener('submit', async e => {
  e.preventDefault();
  loginError.textContent = '';
  loginBtn.disabled = true;

  try {
    const res = await fetch('/api/login', {
      method:  'POST',
      headers: {'Content-Type': 'application/json'},
      body:    JSON.stringify({
        username: inpUser.value.trim(),
        password: inpPass.value,
      }),
    });

    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      loginError.textContent = body.error || (res.status === 401
        ? 'invalid credentials'
        : 'server error — try again');
      loginBtn.disabled = false;
      inpPass.focus();
      return;
    }

    const data = await res.json();
    authToken = data.token;
    authUser  = data.username;
    localStorage.setItem('as-token', authToken);
    localStorage.setItem('as-user',  authUser);

    showTerminal(authUser);
    connectWS(authToken);

  } catch (err) {
    loginError.textContent = 'network error — check your connection';
    loginBtn.disabled = false;
  }
});

// ── Signup flow ──────────────────────────────────────────────────────────────
const signupForm  = document.getElementById('signup-form');
const confirmForm = document.getElementById('confirm-form');
const suUser  = document.getElementById('inp-su-user');
const suEmail = document.getElementById('inp-su-email');
const suPass  = document.getElementById('inp-su-pass');
const suError = document.getElementById('signup-error');
const suBtn   = document.getElementById('signup-btn');
const cfCode  = document.getElementById('inp-cf-code');
const cfError = document.getElementById('confirm-error');
const cfBtn   = document.getElementById('confirm-btn');

// The username a pending confirmation belongs to.
let pendingUser = '';

function showForm(which) {
  loginForm.hidden   = which !== 'login';
  signupForm.hidden  = which !== 'signup';
  confirmForm.hidden = which !== 'confirm';
  loginError.textContent = '';
  suError.textContent = ''; suError.className = '';
  cfError.textContent = ''; cfError.className = '';
}

document.getElementById('show-signup').addEventListener('click', () => {
  showForm('signup'); suUser.focus();
});
document.getElementById('signup-back').addEventListener('click', () => {
  showForm('login'); inpUser.focus();
});
document.getElementById('confirm-back').addEventListener('click', () => {
  showForm('login'); inpUser.focus();
});

signupForm.addEventListener('submit', async e => {
  e.preventDefault();
  suError.textContent = ''; suError.className = '';
  suBtn.disabled = true;
  try {
    const res = await fetch('/api/signup', {
      method:  'POST',
      headers: {'Content-Type': 'application/json'},
      body:    JSON.stringify({
        username: suUser.value.trim(),
        email:    suEmail.value.trim(),
        password: suPass.value,
      }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      suError.textContent = body.error || 'signup failed — try again';
      suBtn.disabled = false;
      return;
    }
    // Pending: move to code entry.
    pendingUser = suUser.value.trim();
    suBtn.disabled = false;
    showForm('confirm');
    cfError.textContent = 'Check your email for a 6-digit code.';
    cfError.className = 'ok';
    cfCode.focus();
  } catch (err) {
    suError.textContent = 'network error — check your connection';
    suBtn.disabled = false;
  }
});

confirmForm.addEventListener('submit', async e => {
  e.preventDefault();
  cfError.textContent = ''; cfError.className = '';
  cfBtn.disabled = true;
  try {
    const res = await fetch('/api/confirm', {
      method:  'POST',
      headers: {'Content-Type': 'application/json'},
      body:    JSON.stringify({ username: pendingUser, code: cfCode.value.trim() }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      cfError.textContent = body.error || 'confirmation failed';
      cfBtn.disabled = false;
      return;
    }
    // Confirmed: back to login, prefilled.
    cfBtn.disabled = false;
    showForm('login');
    inpUser.value = pendingUser;
    loginError.textContent = 'Account confirmed — please log in.';
    inpPass.focus();
  } catch (err) {
    cfError.textContent = 'network error — check your connection';
    cfBtn.disabled = false;
  }
});

// ── WebSocket + xterm.js ─────────────────────────────────────────────────────
function connectWS(token) {
  if (activeWS) activeWS.close();

  term = new Terminal({
    fontFamily:       '"Share Tech Mono","Fira Code","Courier New",monospace',
    fontSize:         14,
    lineHeight:       1.2,
    theme:            xtermThemes[currentTheme],
    cursorBlink:      true,
    cursorStyle:      'block',
    scrollback:       5000,
    convertEol:       false,
    allowProposedApi: true,
  });

  fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(termEl);
  fitAddon.fit();

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(`${proto}//${location.host}/ws?token=${encodeURIComponent(token)}`);
  ws.binaryType = 'arraybuffer';
  activeWS = ws;

  ws.onopen = () => {
    sendResize(ws);
    term.onData(data => {
      if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data));
    });
    term.onResize(() => sendResize(ws));
  };

  ws.onmessage = e => {
    if (typeof e.data === 'string') {
      // Text frames are JSON control messages (call signaling).
      let msg;
      try { msg = JSON.parse(e.data); } catch { return; }
      const fn = controlHandlers.get(msg.type);
      if (fn) fn(msg);
      return;
    }
    term.write(new Uint8Array(e.data));
  };

  ws.onclose = e => {
    if (e.code !== 1000) {
      // Unexpected close
      term.write('\r\n\x1b[2m(disconnected)\x1b[0m\r\n');
    }
    activeWS = null;
  };

  ws.onerror = () => {
    if (ws.readyState !== WebSocket.OPEN) {
      localStorage.removeItem('as-token');
      localStorage.removeItem('as-user');
      showLogin();
      loginError.textContent = 'connection failed — please log in again';
    }
  };

  // Resize observer
  const ro = new ResizeObserver(() => {
    if (fitAddon) fitAddon.fit();
  });
  ro.observe(termEl);
}

function sendResize(ws) {
  if (!term || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({type: 'resize', cols: term.cols, rows: term.rows}));
}

// ── Disconnect ───────────────────────────────────────────────────────────────
sbDisconnect.addEventListener('click', async () => {
  const t = authToken;
  authToken = null;
  authUser  = null;
  localStorage.removeItem('as-token');
  localStorage.removeItem('as-user');
  if (activeWS) { activeWS.close(1000); activeWS = null; }
  // Fire-and-forget logout
  if (t) fetch('/api/logout', {method:'DELETE', headers:{'X-Token': t}}).catch(() => {});
  showLogin();
});

// ── Auto-reconnect on load if token saved ───────────────────────────────────
if (authToken && authUser) {
  showTerminal(authUser);
  connectWS(authToken);
} else {
  showLogin();
}
