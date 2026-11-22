#!/usr/bin/env node
'use strict';

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const readline = require('readline/promises');

const APP_ID = 489823;
const APP_NAME = 'nova_ai';
const PACKAGE_NAME = 'com.larus.wolf';
const VERSION_NAME = '15.9.0';
const VERSION_CODE = 15090030;
const USER_AGENT = 'com.larus.wolf/15090030 (Linux; U; Android 14; en_US; Pixel 7; Build/UQ1A.240205.004)';
const API_BASE = 'https://api-normal-i18n.dola.com';
const DEVICE_REGISTER_URL = 'https://ichannel-i18n.ciciai.com/service/2/device_register/';
const DEFAULT_BOT_ID = '7241547611541340167';
// DOLA_SESSION_PATH menyimpan device/bot (boleh dipakai bersama).
const SESSION_PATH = process.env.DOLA_SESSION_PATH
  ? path.resolve(process.env.DOLA_SESSION_PATH)
  : path.join(__dirname, 'dola_session.json');
// DOLA_CONV_PATH menyimpan konteks percakapan per chat, terpisah dari device.
const CONV_PATH = process.env.DOLA_CONV_PATH ? path.resolve(process.env.DOLA_CONV_PATH) : null;
const REQUEST_TIMEOUT = 180000;
const LIMIT_CODES = new Set([710022021, 710022020, 710022013, 710012001]);

const randHex = (len) => crypto.randomBytes(len).toString('hex');
const makeId = (tag) => `${tag}-${Date.now()}-${randHex(4)}`;

function makeErr(msg, code, data) {
  const err = new Error(msg);
  if (code !== undefined) err.code = code;
  if (data !== undefined) err.data = data;
  return err;
}

function emptySession() {
  return {
    device: null,
    bot_id: null,
    sec_user_id: null,
    tour_ab_param: null,
    conversation: null,
    history: [],
    stats: { replies: 0 },
    created_at: Date.now(),
    updated_at: Date.now(),
  };
}

function loadSession() {
  try {
    const raw = JSON.parse(fs.readFileSync(SESSION_PATH, 'utf8'));
    if (!raw || typeof raw !== 'object') return emptySession();
    return {
      ...emptySession(),
      ...raw,
      device: raw.device && typeof raw.device === 'object' ? raw.device : null,
      conversation: raw.conversation && typeof raw.conversation === 'object' ? raw.conversation : null,
      history: Array.isArray(raw.history) ? raw.history : [],
      stats: { replies: Number(raw.stats?.replies) || 0 },
    };
  } catch {
    return emptySession();
  }
}

function loadConversation(sess) {
  if (!CONV_PATH) return;
  try {
    const conv = JSON.parse(fs.readFileSync(CONV_PATH, 'utf8'));
    sess.conversation = conv && typeof conv.conversation === 'object' ? conv.conversation : null;
  } catch {
    sess.conversation = null;
  }
}

let session = loadSession();
loadConversation(session);

function saveSession() {
  session.updated_at = Date.now();
  fs.mkdirSync(path.dirname(SESSION_PATH), { recursive: true });
  if (CONV_PATH) {
    fs.mkdirSync(path.dirname(CONV_PATH), { recursive: true });
    fs.writeFileSync(CONV_PATH, JSON.stringify({ conversation: session.conversation }, null, 2), { mode: 0o600 });
    try { fs.chmodSync(CONV_PATH, 0o600); } catch {}
  }
  fs.writeFileSync(SESSION_PATH, JSON.stringify(session, null, 2), { mode: 0o600 });
  try { fs.chmodSync(SESSION_PATH, 0o600); } catch {}
}

function isDeviceValid(dev) {
  return Boolean(
    dev &&
    /^\d+$/.test(String(dev.device_id || '')) &&
    /^\d+$/.test(String(dev.install_id || '')) &&
    dev.ssid
  );
}

function extractBigId(raw, data, key) {
  const strVal = data?.[`${key}_str`];
  if (strVal && /^\d+$/.test(String(strVal))) return String(strVal);
  const m = raw.match(new RegExp(`"${key}"\\s*:\\s*(\\d+)`));
  if (m) return m[1];
  if (data?.[key] !== undefined && /^\d+$/.test(String(data[key]))) return String(data[key]);
  return null;
}

async function reqJson(url, opts = {}, timeout = REQUEST_TIMEOUT) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeout);
  try {
    const res = await fetch(url, { ...opts, signal: ac.signal });
    const raw = await res.text();
    let data = null;
    try { data = JSON.parse(raw); } catch {}
    if (!res.ok) throw makeErr(`HTTP ${res.status}: ${raw.slice(0, 300)}`, res.status, data || raw);
    if (!data) throw makeErr('Invalid JSON response', res.status, raw);
    return { status: res.status, data, raw };
  } catch (err) {
    if (err.name === 'AbortError') throw makeErr(`Request timeout (${timeout}ms)`);
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

async function registerDevice() {
  const openudid = randHex(16);
  const clientudid = crypto.randomUUID();
  const cdid = crypto.randomUUID();

  const payload = {
    header: {
      aid: APP_ID,
      app_name: APP_NAME,
      package: PACKAGE_NAME,
      app_version: VERSION_NAME,
      app_version_minor: '',
      version_code: VERSION_CODE,
      update_version_code: VERSION_CODE,
      manifest_version_code: VERSION_CODE,
      device_platform: 'android',
      os: 'android',
      os_version: '14',
      os_api: '34',
      device_type: 'Pixel 7',
      device_brand: 'google',
      device_model: 'Pixel 7',
      device_manufacturer: 'Google',
      channel: 'googleplay',
      tweaked_channel: '',
      resolution: '1080x2400',
      dpi: 420,
      language: 'id',
      region: 'ID',
      app_language: 'id',
      app_region: 'ID',
      timezone: 7,
      access: 'wifi',
      carrier: '',
      sim_region: '',
      device_category: 'phone',
      openudid,
      clientudid,
      cdid,
      sig_hash: '',
      sdk_version: '4.1.0',
      sdk_target_version: 29,
      guest_mode: 1,
      is_guest: 1,
      not_request_sender: 1,
      release_build: '',
      user_agent: USER_AGENT,
      google_aid: '',
      gaid_limited: 0,
      custom: { real_package_name: PACKAGE_NAME },
    },
    magic_tag: 'ss_app_log',
    _gen_time: Date.now(),
  };

  const { data, raw } = await reqJson(DEVICE_REGISTER_URL, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'User-Agent': USER_AGENT,
      'x-tt-request-tag': 't=0;n=0',
    },
    body: JSON.stringify(payload),
  });

  const deviceId = extractBigId(raw, data, 'device_id');
  const installId = extractBigId(raw, data, 'install_id');
  if (!deviceId || !installId || !data?.ssid) {
    throw makeErr('Gagal register device', data?.code, data);
  }

  return {
    device_id: deviceId,
    install_id: installId,
    ssid: String(data.ssid),
    openudid,
    clientudid,
    cdid,
    created_at: Date.now(),
  };
}

function buildQuery(dev) {
  return new URLSearchParams({
    aid: String(APP_ID),
    app_name: APP_NAME,
    version_code: String(VERSION_CODE),
    version_name: VERSION_NAME,
    manifest_version_code: String(VERSION_CODE),
    update_version_code: String(VERSION_CODE),
    device_platform: 'android',
    os: 'android',
    os_version: '14',
    os_api: '34',
    device_type: 'Pixel 7',
    device_brand: 'google',
    channel: 'googleplay',
    region: 'ID',
    language: 'id',
    device_id: String(dev.device_id),
    install_id: String(dev.install_id),
    iid: String(dev.install_id),
    ssid: String(dev.ssid),
  }).toString().replace(/\+/g, '%20');
}

function buildHeaders(isStream) {
  const h = {
    'Content-Type': isStream ? 'application/json; encoding=utf-8' : 'application/json',
    'User-Agent': USER_AGENT,
    'x-tt-appid': String(APP_ID),
    'x-tt-app-name': APP_NAME,
  };
  if (isStream) h.Accept = 'text/event-stream';
  return h;
}

async function fetchCore(dev) {
  const url = `${API_BASE}/alice/user/launch/core?${buildQuery(dev)}&source=local_main&has_cache=0`;
  const { data } = await reqJson(url, {
    method: 'POST',
    headers: buildHeaders(false),
    body: JSON.stringify({
      is_first_cold_start: true,
      is_first_install_cold_start: true,
      cold_start_timestamp_ms: Date.now(),
    }),
  });
  if (!data || Number(data.code) !== 0 || !data.data) {
    throw makeErr(data?.msg || 'Core launch gagal', data?.code, data);
  }
  return data.data;
}

async function ensureDevice(opts = {}) {
  const force = Boolean(opts.force);
  if (!force && isDeviceValid(session.device)) {
    if (!session.bot_id) session.bot_id = DEFAULT_BOT_ID;
    return session.device;
  }

  process.stderr.write(force ? 'Registering new device...\n' : 'Registering device...\n');
  session.device = await registerDevice();
  session.bot_id = null;
  session.sec_user_id = null;
  session.tour_ab_param = null;
  session.conversation = null;
  session.stats = { replies: 0 };
  saveSession();

  try {
    const core = await fetchCore(session.device);
    session.bot_id = core.assistant_bot_id || null;
    session.sec_user_id = core.sec_user_id || null;
    session.tour_ab_param = core.tour_ab_param || null;
  } catch (err) {
    process.stderr.write(`Core warning: ${err.message}\n`);
  }

  if (!session.bot_id) session.bot_id = DEFAULT_BOT_ID;
  saveSession();
  return session.device;
}

function startNewConversation() {
  session.conversation = null;
  saveSession();
}

function unwrapText(raw) {
  if (typeof raw !== 'string') return '';
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed.text === 'string') return parsed.text;
    if (typeof parsed.content === 'string') return parsed.content;
  } catch {}
  return raw;
}

function createSseReader(onEvent) {
  let buf = '';
  const state = { done: false };

  function handleBlock(block) {
    let ev = 'message';
    const lines = [];
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith('event:')) ev = line.slice(6).trim();
      else if (line.startsWith('data:')) lines.push(line.slice(5).trim());
    }
    if (ev === 'done') {
      state.done = true;
      return;
    }
    if (lines.length) onEvent(ev, lines.join('\n'));
  }

  return {
    state,
    push(chunk) {
      buf += chunk;
      while (!state.done) {
        const m = buf.match(/\r?\n\r?\n/);
        if (!m || m.index === undefined) break;
        const block = buf.slice(0, m.index);
        buf = buf.slice(m.index + m[0].length);
        handleBlock(block);
      }
    },
    end() {
      if (buf.trim()) handleBlock(buf);
      buf = '';
    },
  };
}

async function sendOnce(prompt, opts) {
  const dev = session.device;
  if (!isDeviceValid(dev)) throw makeErr('Device belum siap');

  const locMsgId = makeId('loc');
  const locConvId = session.conversation?.local_id || makeId('lcid');
  const payload = {
    cmd: 100,
    sequence_id: locMsgId,
    uplink_body: {
      send_message_body: {
        conversation_id: session.conversation?.id ? String(session.conversation.id) : '',
        conversation_type: 3,
        sender_id: '0',
        content_type: 1,
        content: prompt,
        local_message_id: locMsgId,
        local_conversation_id: locConvId,
        bot_id: String(session.bot_id || DEFAULT_BOT_ID),
        bot_type: 0,
        status: 0,
        create_time: Math.floor(Date.now() / 1000),
      },
    },
    channel: 3,
    version: '1',
  };

  const streamOut = Boolean(opts.stream);
  const timeout = Number(opts.timeout) || REQUEST_TIMEOUT;
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeout);
  const url = `${API_BASE}/im/sse/send/message?flow_im_arch=v2&${buildQuery(dev)}`;

  let res;
  try {
    res = await fetch(url, {
      method: 'POST',
      headers: buildHeaders(true),
      body: JSON.stringify(payload),
      signal: ac.signal,
    });
  } catch (err) {
    clearTimeout(timer);
    if (err.name === 'AbortError') throw makeErr(`Request timeout (${timeout}ms)`);
    throw err;
  }

  if (!res.ok) {
    const raw = await res.text();
    clearTimeout(timer);
    throw makeErr(`HTTP ${res.status}: ${raw.slice(0, 300)}`, res.status, raw);
  }

  let text = '';
  let replyMsgId = null;
  let ack = null;
  let streamErr = null;
  let nextSeq = null;
  const pending = new Map();

  const writeOut = (val) => {
    if (!val) return;
    text += val;
    if (streamOut) process.stdout.write(val);
  };

  const flush = (all = false) => {
    if (nextSeq === null && pending.size) nextSeq = Math.min(...pending.keys());
    while (nextSeq !== null && pending.has(nextSeq)) {
      writeOut(pending.get(nextSeq));
      pending.delete(nextSeq);
      nextSeq += 1;
    }
    if (all && pending.size) {
      for (const k of [...pending.keys()].sort((a, b) => a - b)) writeOut(pending.get(k));
      pending.clear();
    }
  };

  const sse = createSseReader((ev, raw) => {
    if (ev !== 'json' || streamErr) return;
    let msg;
    try { msg = JSON.parse(raw); } catch { return; }

    const cmd = Number(msg.cmd);
    if (cmd === 100) {
      const a = msg.downlink_body?.send_message_ack_downlink_body;
      if (a) {
        ack = {
          conversation_id: a.conversation_id || null,
          message_id: a.message_id || null,
          last_section_id: a.last_section_id || null,
          message_index: a.message_index ?? null,
          local_message_id: a.local_message_id || null,
        };
      }
      if (msg.status_code && Number(msg.status_code) !== 0) {
        streamErr = makeErr(msg.status_desc || `Dola error ${msg.status_code}`, msg.status_code, msg);
      }
    } else if (cmd === 200) {
      const item = msg.downlink_body?.new_message_notify?.message;
      if (item && Number(item.user_type) === 2 && Number(item.content_type) === 1) {
        replyMsgId = item.message_id || replyMsgId;
        writeOut(unwrapText(item.content));
      }
    } else if (cmd === 300) {
      const chunk = msg.downlink_body?.fetch_chunk_message_downlink_body;
      if (chunk) {
        replyMsgId = chunk.message_id || replyMsgId;
        const val = unwrapText(chunk.content);
        const seq = Number(chunk.seq_no);
        if (Number.isFinite(seq)) {
          if (nextSeq !== null && seq < nextSeq) return;
          pending.set(seq, val);
          flush();
        } else {
          writeOut(val);
        }
      }
    } else if (cmd === 50400) {
      const end = msg.downlink_body?.reply_end_downlink_body;
      if (end?.message_id) replyMsgId = end.message_id;
    }
  });

  try {
    if (!res.body) throw makeErr('Empty response body');
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    while (!sse.state.done && !streamErr) {
      const { done, value } = await reader.read();
      if (done) break;
      sse.push(dec.decode(value, { stream: true }));
    }
    if (!streamErr) {
      sse.push(dec.decode());
      sse.end();
      flush(true);
    }
    try { await reader.cancel(); } catch {}
  } catch (err) {
    if (err.name === 'AbortError') throw makeErr(`Request timeout (${timeout}ms)`);
    throw err;
  } finally {
    clearTimeout(timer);
  }

  if (streamErr) {
    streamErr.partial = Boolean(text);
    throw streamErr;
  }
  if (!text.trim()) throw makeErr('Empty reply from Dola');

  const convId = ack?.conversation_id || session.conversation?.id || null;
  if (convId) {
    session.conversation = {
      id: String(convId),
      local_id: locConvId,
      last_section_id: ack?.last_section_id
        ? String(ack.last_section_id)
        : session.conversation?.last_section_id || null,
      message_index: ack?.message_index ?? session.conversation?.message_index ?? null,
      updated_at: Date.now(),
    };
  }

  session.stats.replies = (Number(session.stats.replies) || 0) + 1;
  session.history.push(
    { role: 'user', text: prompt, at: Date.now() },
    { role: 'assistant', text, message_id: replyMsgId, at: Date.now() }
  );
  if (session.history.length > 200) session.history = session.history.slice(-200);
  saveSession();

  return {
    text,
    conversation_id: convId ? String(convId) : null,
    message_id: replyMsgId,
    bot_id: String(session.bot_id || DEFAULT_BOT_ID),
    device_id: String(dev.device_id),
    replies: session.stats.replies,
  };
}

async function sendPrompt(prompt, opts = {}) {
  await ensureDevice();
  let retried = false;
  while (true) {
    try {
      return await sendOnce(prompt, opts);
    } catch (err) {
      if (!retried && opts.autoReset !== false && !err.partial && LIMIT_CODES.has(Number(err.code))) {
        process.stderr.write(`Limit (${err.code}), rotating device...\n`);
        await ensureDevice({ force: true });
        retried = true;
        continue;
      }
      throw err;
    }
  }
}

function printDevice() {
  console.log(JSON.stringify({
    device_id: session.device?.device_id || null,
    install_id: session.device?.install_id || null,
    ssid: session.device?.ssid || null,
    bot_id: session.bot_id,
    sec_user_id: session.sec_user_id,
    conversation: session.conversation,
    replies: session.stats.replies,
    tour_ab_param: session.tour_ab_param,
  }, null, 2));
}

function printHistory() {
  if (!session.history.length) return console.log('No history');
  for (const item of session.history) {
    console.log(`${item.role === 'user' ? 'You' : 'Dola'}: ${item.text}\n`);
  }
}

function printHelp() {
  console.log(`Usage:
  node dola.js "<prompt>"
  node dola.js ask "<prompt>"
  node dola.js chat
  node dola.js new [prompt]
  node dola.js device
  node dola.js history

Flags:
  -n, --new          new conversation
  --new-device       register new device
  --json             output JSON
  --no-stream        disable streaming output
  --timeout <ms>     request timeout (default: 180000)`);
}

function parseCli(argv) {
  const args = [];
  const flags = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '-h' || a === '--help') flags.help = true;
    else if (a === '-n' || a === '--new') flags.new = true;
    else if (a === '--new-device' || a === '--reset-device') flags.newDevice = true;
    else if (a === '--device') flags.device = true;
    else if (a === '--history') flags.history = true;
    else if (a === '--json') flags.json = true;
    else if (a === '--no-stream') flags.stream = false;
    else if (a === '--stream') flags.stream = true;
    else if (a === '--timeout' || a.startsWith('--timeout=')) {
      const val = Number(a === '--timeout' ? argv[++i] : a.slice(10));
      if (!Number.isFinite(val) || val <= 0) throw new Error('Invalid timeout');
      flags.timeout = val;
    } else if (a.startsWith('-')) {
      throw new Error(`Unknown flag: ${a}`);
    } else {
      args.push(a);
    }
  }
  return { args, flags };
}

async function readStdin() {
  let out = '';
  process.stdin.setEncoding('utf8');
  for await (const chunk of process.stdin) out += chunk;
  return out;
}

async function runChat() {
  await ensureDevice();
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  console.log('Dola CLI (/new, /device, /history, /exit)');
  while (true) {
    let line;
    try { line = await rl.question('> '); } catch { break; }
    const input = line.trim();
    if (!input) continue;
    if (input === '/exit' || input === '/quit') break;
    if (input === '/new') {
      startNewConversation();
      console.log('New conversation.');
      continue;
    }
    if (input === '/device') {
      printDevice();
      continue;
    }
    if (input === '/history') {
      printHistory();
      continue;
    }
    try {
      const res = await sendPrompt(input, { stream: true, autoReset: true });
      if (!res.text.endsWith('\n')) process.stdout.write('\n');
    } catch (err) {
      process.stderr.write(`Error: ${err.message}\n`);
    }
  }
  rl.close();
}

async function main() {
  const { args, flags } = parseCli(process.argv.slice(2));
  if (flags.help) return printHelp();

  const cmd = (args[0] || '').toLowerCase();
  const showDevice = flags.device || cmd === 'device';
  const showHistory = flags.history || cmd === 'history';

  if (flags.newDevice && !showDevice && !showHistory && !args.length) {
    await ensureDevice({ force: true });
    return printDevice();
  }
  if (showDevice) {
    await ensureDevice({ force: Boolean(flags.newDevice) });
    return printDevice();
  }
  if (showHistory) return printHistory();

  if (flags.newDevice) await ensureDevice({ force: true });
  if (cmd === 'chat') return runChat();

  if (cmd === 'new' || flags.new) {
    startNewConversation();
    if (args.length <= (cmd === 'new' ? 1 : 0) && !flags.json) {
      return console.log('New conversation.');
    }
  }

  let prompt = (cmd === 'ask' || cmd === 'new' ? args.slice(1) : args).join(' ').trim();
  if (!prompt && !process.stdin.isTTY) prompt = (await readStdin()).trim();
  if (!prompt) return printHelp();

  await ensureDevice();
  const stream = flags.stream !== false && !flags.json;
  const res = await sendPrompt(prompt, { stream, timeout: flags.timeout, autoReset: true });

  if (flags.json) console.log(JSON.stringify(res, null, 2));
  else if (!stream) console.log(res.text);
  else process.stdout.write('\n');
}

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message);
    if (err.data && typeof err.data === 'object') {
      try { console.error(JSON.stringify(err.data, null, 2)); } catch {}
    }
    process.exit(1);
  });
}

module.exports = {
  sendPrompt,
  ensureDevice,
  startNewConversation,
  printDevice,
  printHistory,
  loadSession,
  saveSession,
  registerDevice,
  fetchCore,
  SESSION_PATH,
  DEFAULT_BOT_ID,
  APP_ID,
  APP_NAME,
};
