#!/usr/bin/env node
'use strict';

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const axios = require('axios');

const BASE_URL = 'https://api.sonivamusic.com/musicai/';
const FIREBASE_API_KEY = 'AIzaSyA5IwVHBnETgpJ-gwhJSz2YP0giDOAUFR0';
const FIREBASE_SIGNUP_URL = `https://identitytoolkit.googleapis.com/v1/accounts:signUp?key=${FIREBASE_API_KEY}`;
const FIREBASE_REFRESH_URL = `https://securetoken.googleapis.com/v1/token?key=${FIREBASE_API_KEY}`;
const APP_VERSION = '2.0.3';
const VERSION_CODE = '146';
const USER_AGENT = 'SonivaMusic/2.0.3 (build:146; Android 14; Google Pixel 7)';
const SESSION_PATH = path.join(__dirname, 'aimusic_session.json');
const REQUEST_TIMEOUT = 120000;
const TOKEN_MARGIN_MS = 5 * 60 * 1000;
const DEFAULT_POLL_INTERVAL_MS = 5000;
const DEFAULT_POLL_TIMEOUT_MS = 10 * 60 * 1000;

const GENRES = [
  'Pop', 'Rap', 'Rock', 'EDM', 'R&B', 'Hip-hop', 'Country', 'Latin', 'K-Pop',
  'Classical', 'Jazz', 'Reggae', 'Indie', 'House', 'Afro House', 'Folk', 'Trance',
  'Dance', 'Soul', 'Blues', 'Metal', 'Funk', 'Downtempo', 'Electro Pop',
  'Electro Techno', 'Electronic Grunge', 'Celtic', 'Jungle', 'Electro-Classical',
  'Acoustic', 'Dubstep', 'Disco', 'Trap', 'Drum’n’bass', 'Salsa', 'Tango',
  'Deathcore', 'Blues Rock', 'Gospel', 'Drill', 'Phonk', 'Afrobeats', 'Punk',
  'Amapiano', 'Hardstyle', 'Reggaeton', 'Dancehall', 'Emo', 'Alternative',
  'Schlager', 'Manele', 'Sertanejo', 'Pagode', 'Corrido', 'Corrido Tumbado',
  'Banda', 'Forró', 'Samba', 'Bachata', 'Cumbia', 'Norteño', 'Mariachi',
  'Arrocha', 'Piseiro', 'Zouk', 'Kizomba', 'Kompa',
];

const MOODS = [
  'Happy', 'Romantic', 'Uplifting', 'Motivational', 'Joyful', 'Melancholic',
  'Confident', 'Productivity', 'Dreamy', 'Depressive', 'Chill', 'Hype', 'Slow',
  'Dark', 'Nostalgic', 'Passionate', 'Spiritual', 'Whimsical', 'Eclectic',
  'Emotion', 'Hard', 'Lyrical', 'Magical', 'Cinematic', 'Anime',
];

const VOCAL_GENDERS = ['female', 'male'];
const RECORD_TYPES = ['studio', 'live'];
const SEARCH_TYPES = {
  popular: 'popular',
  populer: 'popular',
  recent: 'recent',
  new: 'recent',
  baru: 'recent',
  trending: 'trending',
  clips: 'clips',
  clip: 'clips',
  klip: 'clips',
};

const GENRE_ALIASES = {
  'hip hop': 'Hip-hop',
  hiphop: 'Hip-hop',
  rnb: 'R&B',
  'r and b': 'R&B',
  kpop: 'K-Pop',
  'drum and bass': 'Drum’n’bass',
  'drum n bass': 'Drum’n’bass',
  dnb: 'Drum’n’bass',
  'electro classical': 'Electro-Classical',
  afrobeat: 'Afrobeats',
  'afro beats': 'Afrobeats',
};

const MOOD_ALIASES = {
  energetic: 'Hype',
  energy: 'Hype',
  focus: 'Productivity',
  relaxing: 'Chill',
  relax: 'Chill',
};

const XOR_KEY = Buffer.from('3a1c2ou68jox9dlj3v', 'utf8');
const IBJ_A = Buffer.from([94, 86, 68, 22, 67, 88, 1, 95, 13, 82, 30, 72, 8, 8, 91, 1]);
const IBJ_B = Buffer.from([94, 81, 94, 10, 92, 28, 71, 87, 78, 2, 9, 10, 72, 14, 92, 27, 92, 14, 4, 15]);
const IBJ_C = Buffer.from([85, 86, 4, 1, 84, 89, 71, 82, 12, 95, 90, 27, 8, 83, 13, 94]);
const FI0_B = Buffer.from([5, 88, 1, 80, 2, 87, 23, 83, 8, 94, 10, 26, 12, 7, 9, 94]);
const X2J_A = Buffer.from([
  91, 189, 83, 100, 202, 30, 78, 248, 167, 214, 46, 109, 31, 18, 118, 56,
  3, 215, 108, 125, 214, 1, 176, 251, 43, 211, 69, 68, 27, 120, 183, 92,
  219, 170, 156, 223, 64, 57, 28, 35, 230, 143, 225, 5, 31, 44, 249, 107,
  175, 37, 108, 238, 236, 181, 1, 199, 78, 36, 96, 142, 82, 123, 154, 72,
]);

function xorWithKey(input) {
  const output = Buffer.alloc(input.length);
  for (let i = 0; i < input.length; i += 1) {
    output[i] = input[i] ^ XOR_KEY[i % XOR_KEY.length];
  }
  return output;
}

function deriveKey(encrypted) {
  const source = xorWithKey(IBJ_A).toString('utf8')
    + xorWithKey(IBJ_B).toString('utf8')
    + 'com.sonivamusic.ai';
  const digest = crypto.createHash('sha512').update(source, 'utf8').digest();
  const key = Buffer.alloc(encrypted.length);
  for (let i = 0; i < encrypted.length; i += 1) {
    key[i] = encrypted[i] ^ digest[i];
  }
  return key;
}

const SIGNING_KEY = deriveKey(X2J_A);
const SIGNATURE_SALT = xorWithKey(FI0_B).toString('utf8') + xorWithKey(IBJ_C).toString('utf8');

let session = loadSession();
let ensurePromise = null;

function loadSession() {
  try {
    return JSON.parse(fs.readFileSync(SESSION_PATH, 'utf8'));
  } catch (error) {
    return {};
  }
}

function saveSession() {
  fs.writeFileSync(SESSION_PATH, JSON.stringify(session, null, 2), { mode: 0o600 });
  fs.chmodSync(SESSION_PATH, 0o600);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function normalizeLabel(value) {
  return String(value)
    .trim()
    .toLowerCase()
    .replace(/[’']/g, "'")
    .replace(/\s+/g, ' ');
}

function resolveOption(value, list, aliases, name) {
  const target = normalizeLabel(value);
  if (aliases[target]) return aliases[target];
  const found = list.find((item) => normalizeLabel(item) === target);
  if (found) return found;
  throw new Error(`Invalid ${name} "${value}". Valid values: ${list.join(', ')}`);
}

function resolveGenre(value) {
  return resolveOption(value, GENRES, GENRE_ALIASES, 'genre');
}

function resolveMood(value) {
  return resolveOption(value, MOODS, MOOD_ALIASES, 'mood');
}

function resolveVocalGender(value) {
  const target = String(value).trim().toLowerCase();
  if (VOCAL_GENDERS.includes(target)) return target;
  throw new Error(`Invalid vocal gender "${value}". Valid values: ${VOCAL_GENDERS.join(', ')}`);
}

function resolveRecordType(value) {
  const target = String(value).trim().toLowerCase();
  if (RECORD_TYPES.includes(target)) return target;
  throw new Error(`Invalid record type "${value}". Valid values: ${RECORD_TYPES.join(', ')}`);
}

async function firebaseSignUp() {
  const response = await axios.post(
    FIREBASE_SIGNUP_URL,
    { returnSecureToken: true },
    {
      headers: { 'Content-Type': 'application/json' },
      timeout: 30000,
      validateStatus: () => true,
    },
  );
  if (response.status >= 400) {
    const message = response.data?.error?.message || JSON.stringify(response.data);
    throw new Error(`Firebase anonymous sign-up failed: ${message}`);
  }
  const data = response.data;
  session.id_token = data.idToken;
  session.refresh_token = data.refreshToken;
  session.firebase_uid = data.localId;
  session.token_expires_at = Date.now() + Number(data.expiresIn || 3600) * 1000;
  session.bound = false;
  saveSession();
}

async function refreshFirebase() {
  if (!session.refresh_token) throw new Error('Firebase refresh token is missing');
  const body = new URLSearchParams({
    grant_type: 'refresh_token',
    refresh_token: session.refresh_token,
  }).toString();
  const response = await axios.post(
    FIREBASE_REFRESH_URL,
    body,
    {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      timeout: 30000,
      validateStatus: () => true,
    },
  );
  if (response.status >= 400) {
    const message = response.data?.error?.message || JSON.stringify(response.data);
    throw new Error(`Firebase token refresh failed: ${message}`);
  }
  const data = response.data;
  session.id_token = data.id_token || data.idToken;
  session.refresh_token = data.refresh_token || data.refreshToken || session.refresh_token;
  session.firebase_uid = data.user_id || session.firebase_uid;
  session.token_expires_at = Date.now() + Number(data.expires_in || data.expiresIn || 3600) * 1000;
  saveSession();
}

function buildHeaders(messageId, timestamp, signature) {
  const headers = {
    'User-Agent': USER_AGENT,
    'X-Device-ID': session.device_id,
    'X-Request-Time': timestamp,
    'X-Message-ID': messageId,
    'X-Platform': 'android',
    'X-App-Version': APP_VERSION,
    'X-Version-Code': VERSION_CODE,
    'X-Signature-ID': signature,
    'X-Request-ID': signature,
    'Accept-Language': 'en-US',
    'Content-Type': 'application/json',
  };
  if (session.id_token) headers.Authorization = `Bearer ${session.id_token}`;
  return headers;
}

async function signedRequest(method, endpoint, body, retry = true) {
  const messageId = crypto.randomUUID();
  const timestamp = String(Date.now());
  const signature = crypto
    .createHmac('sha256', SIGNING_KEY)
    .update(messageId + SIGNATURE_SALT + session.device_id + timestamp, 'utf8')
    .digest('base64');
  const response = await axios.request({
    method,
    url: BASE_URL + endpoint,
    data: body,
    headers: buildHeaders(messageId, timestamp, signature),
    timeout: REQUEST_TIMEOUT,
    validateStatus: () => true,
  });
  if (response.status === 401 && retry) {
    try {
      await refreshFirebase();
    } catch (error) {
      await firebaseSignUp();
    }
    return signedRequest(method, endpoint, body, false);
  }
  if (response.status >= 400) {
    const data = response.data;
    const detail = typeof data === 'string' ? data : JSON.stringify(data);
    const code = data && (data.error_code || data.error || data.message);
    const error = new Error(
      `${method} ${endpoint} failed with HTTP ${response.status}${code ? ` (${code})` : ''}: ${detail}`,
    );
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return response.data;
}

async function bindIdentity(force = false) {
  if (!force && session.bound) return;
  await signedRequest('POST', 'v2/identity/bind', { legacy_user_id: session.user_id });
  session.bound = true;
  saveSession();
}

async function ensureSessionInternal() {
  if (!session.device_id) {
    session.device_id = crypto.randomUUID();
    saveSession();
  }
  if (!session.id_token || !session.refresh_token) {
    await firebaseSignUp();
  } else if (Date.now() >= Number(session.token_expires_at || 0) - TOKEN_MARGIN_MS) {
    try {
      await refreshFirebase();
    } catch (error) {
      await firebaseSignUp();
    }
  }
  if (!session.user_id) {
    const data = await signedRequest('POST', 'v2/register', { device_id: session.device_id });
    session.user_id = data.user_id;
    session.referral_code = data.referral_code;
    session.bound = false;
    saveSession();
  }
  if (!session.bound) await bindIdentity();
}

function ensureSession() {
  if (!ensurePromise) {
    ensurePromise = ensureSessionInternal().finally(() => {
      ensurePromise = null;
    });
  }
  return ensurePromise;
}

async function apiRequest(method, endpoint, body, options = {}) {
  await ensureSession();
  try {
    return await signedRequest(method, endpoint, body);
  } catch (error) {
    const code = error.data && error.data.error_code;
    if (code === 'bind_required' && !options.rebound) {
      await bindIdentity(true);
      return apiRequest(method, endpoint, body, { ...options, rebound: true });
    }
    throw error;
  }
}

function buildSongPayload(options = {}) {
  const hasVocal = options.instrumental ? false : options.hasVocal !== false;
  return {
    mood: resolveMood(options.mood || 'Happy'),
    genre: resolveGenre(options.genre || 'Pop'),
    has_vocal: hasVocal,
    vocal_gender: resolveVocalGender(options.vocalGender || options.vocal_gender || 'female'),
    record_type: resolveRecordType(options.recordType || options.record_type || 'studio'),
    is_dual_song_enabled: Boolean(
      options.isDualSongEnabled ?? options.is_dual_song_enabled ?? options.dual ?? false,
    ),
  };
}

async function getUserInfo() {
  await ensureSession();
  return apiRequest('GET', `v2/users/${session.user_id}/info`);
}

async function generateLyrics(prompt, options = {}) {
  const content = String(prompt || options.content || '').trim();
  if (!content) throw new Error('prompt is required');
  const task = options.task || 'askai';
  const data = await apiRequest('POST', 'v2/lyrics/generate', { task, content });
  return data;
}

async function createSongFromDescription(options = {}) {
  const description = String(
    options.description || options.desc || options.prompt || '',
  ).trim();
  if (!description) throw new Error('description is required');
  const payload = buildSongPayload(options);
  payload.prompt = description;
  await ensureSession();
  const job = await apiRequest('POST', `v2/users/${session.user_id}/songs/prompt`, payload);
  if (options.wait === false) return job;
  return waitForJob(job.job_id, options);
}

async function createSongFromLyrics(options = {}) {
  const lyrics = String(options.lyrics || '').trim();
  const title = String(options.title || '').trim();
  if (!lyrics) throw new Error('lyrics is required');
  if (!title) throw new Error('title is required');
  const payload = buildSongPayload(options);
  payload.lyrics = lyrics;
  payload.title = title;
  await ensureSession();
  const job = await apiRequest('POST', `v2/users/${session.user_id}/songs/lyrics`, payload);
  if (options.wait === false) return job;
  return waitForJob(job.job_id, options);
}

async function getSongDetails(songId) {
  if (!songId) throw new Error('songId is required');
  return apiRequest('GET', `v2/songs/${songId}/details`);
}

async function getSongJob(jobId) {
  if (!jobId) throw new Error('jobId is required');
  return apiRequest('GET', `v2/songs/${jobId}`);
}

async function waitForJob(jobId, options = {}) {
  if (!jobId) throw new Error('jobId is required');
  const intervalMs = options.intervalMs || DEFAULT_POLL_INTERVAL_MS;
  const timeoutMs = options.timeoutMs || DEFAULT_POLL_TIMEOUT_MS;
  const startedAt = Date.now();
  let lastJob = null;
  for (;;) {
    lastJob = await getSongJob(jobId);
    if (typeof options.onProgress === 'function') options.onProgress(lastJob);
    const status = String(lastJob.status || '').toLowerCase();
    if (status === 'complete') {
      const ids = [lastJob.id, lastJob.variant_id].filter(Boolean);
      if (!ids.length) throw new Error(`Job ${jobId} completed without song ids`);
      const songs = [];
      for (const id of ids) {
        try {
          songs.push(await getSongDetails(id));
        } catch (error) {
          songs.push({ id, error: error.message });
        }
      }
      return { job: lastJob, songs };
    }
    if (['error', 'failed', 'cancelled', 'canceled'].includes(status)) {
      throw new Error(
        `Song generation ${status}: ${lastJob.error_message || lastJob.error_type || 'unknown error'}`,
      );
    }
    if (Date.now() - startedAt > timeoutMs) {
      throw new Error(`Timed out waiting for job ${jobId} (last status: ${lastJob.status})`);
    }
    await sleep(intervalMs);
  }
}

function buildSearchQuery(options = {}) {
  const page = Number(options.page || 1);
  const limit = Number(options.limit || 20);
  if (!Number.isInteger(page) || page < 1) throw new Error('page must be a positive integer');
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
    throw new Error('limit must be between 1 and 100');
  }
  return { page, limit };
}

async function search(type, options = {}) {
  const kind = SEARCH_TYPES[String(type || '').trim().toLowerCase()];
  if (!kind) {
    throw new Error(`Invalid search type "${type}". Valid values: popular, recent, trending, clips`);
  }
  const { page, limit } = buildSearchQuery(options);
  const params = new URLSearchParams({ page: String(page), limit: String(limit) });
  if (kind === 'popular' || kind === 'trending') {
    params.set('range', options.range || 'week');
  }
  return apiRequest('GET', `v2/explore/${kind}?${params.toString()}`);
}

async function searchAll(options = {}) {
  const result = {};
  await Promise.all(['popular', 'recent', 'trending', 'clips'].map(async (kind) => {
    result[kind] = await search(kind, options);
  }));
  return result;
}

async function getLibrary(options = {}) {
  const { page, limit } = buildSearchQuery(options);
  const params = new URLSearchParams({
    page: String(page),
    limit: String(limit),
    sortAsc: String(Boolean(options.sortAsc ?? options.sort_asc ?? false)),
    q: String(options.q || ''),
  });
  await ensureSession();
  return apiRequest('GET', `v2/users/${session.user_id}/library?${params.toString()}`);
}

function getSessionSummary() {
  return {
    device_id: session.device_id,
    firebase_uid: session.firebase_uid,
    user_id: session.user_id,
    referral_code: session.referral_code,
    bound: Boolean(session.bound),
    token_expires_at: session.token_expires_at,
    has_refresh_token: Boolean(session.refresh_token),
  };
}

function parseArgs(argv) {
  const positional = [];
  const flags = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg.startsWith('--')) {
      const [rawKey, inline] = arg.slice(2).split('=');
      const key = rawKey.replace(/-([a-z])/g, (_, char) => char.toUpperCase());
      if (inline !== undefined) {
        flags[key] = inline;
      } else if (i + 1 < argv.length && !argv[i + 1].startsWith('--')) {
        flags[key] = argv[i + 1];
        i += 1;
      } else {
        flags[key] = true;
      }
    } else {
      positional.push(arg);
    }
  }
  return { positional, flags };
}

function printHelp() {
  console.log(`Usage:
  node aimusic.js info
  node aimusic.js lyrics "<prompt>"
  node aimusic.js create --desc "<description>" [options]
  node aimusic.js create --lyrics "<lyrics>" --title "<title>" [options]
  node aimusic.js search <popular|recent|trending|clips|all> [--page 1] [--limit 20] [--range week]
  node aimusic.js library [--page 1] [--limit 20] [--q text] [--sort-asc]
  node aimusic.js song <songId>
  node aimusic.js job <jobId>
  node aimusic.js wait <jobId>
  node aimusic.js genres
  node aimusic.js moods
  node aimusic.js session

Create options:
  --genre <name>          default: Pop
  --mood <name>           default: Happy
  --gender <female|male>  default: female
  --record <studio|live>  default: studio
  --instrumental          no vocals
  --dual                  enable dual song
  --no-wait               return job id without polling
  --title <title>         required with --lyrics
  --lyrics-file <path>    read lyrics from file
  --timeout <ms>          poll timeout, default: 600000
  --interval <ms>         poll interval, default: 5000`);
}

async function main() {
  const { positional, flags } = parseArgs(process.argv.slice(2));
  const command = String(positional[0] || '').toLowerCase();

  if (flags.help || command === 'help' || !command) {
    printHelp();
    return;
  }

  if (command === 'genres') {
    console.log(JSON.stringify(GENRES, null, 2));
    return;
  }

  if (command === 'moods') {
    console.log(JSON.stringify(MOODS, null, 2));
    return;
  }

  if (command === 'session') {
    console.log(JSON.stringify(getSessionSummary(), null, 2));
    return;
  }

  if (command === 'info') {
    console.log(JSON.stringify(await getUserInfo(), null, 2));
    return;
  }

  if (command === 'lyrics') {
    const prompt = positional.slice(1).join(' ').trim() || String(flags.prompt || '').trim();
    console.log(JSON.stringify(await generateLyrics(prompt), null, 2));
    return;
  }

  if (command === 'create') {
    let lyrics = flags.lyrics ? String(flags.lyrics) : '';
    if (flags.lyricsFile) lyrics = fs.readFileSync(String(flags.lyricsFile), 'utf8');
    const options = {
      genre: flags.genre,
      mood: flags.mood,
      vocalGender: flags.gender || flags.vocalGender,
      recordType: flags.record || flags.recordType,
      instrumental: Boolean(flags.instrumental || flags.noVocal),
      dual: Boolean(flags.dual || flags.isDualSongEnabled),
      wait: !flags.noWait,
      timeoutMs: flags.timeout ? Number(flags.timeout) : undefined,
      intervalMs: flags.interval ? Number(flags.interval) : undefined,
      onProgress: (job) => {
        process.stderr.write(`status: ${job.status || 'unknown'}${job.title ? ` - ${job.title}` : ''}\n`);
      },
    };
    if (lyrics) {
      options.lyrics = lyrics;
      options.title = flags.title;
      console.log(JSON.stringify(await createSongFromLyrics(options), null, 2));
    } else {
      options.description = flags.desc || flags.description || flags.prompt
        || positional.slice(1).join(' ');
      console.log(JSON.stringify(await createSongFromDescription(options), null, 2));
    }
    return;
  }

  if (command === 'search') {
    const type = String(positional[1] || flags.type || '').toLowerCase();
    if (!type) throw new Error('search type is required (popular, recent, trending, clips, all)');
    const options = {
      page: flags.page,
      limit: flags.limit,
      range: flags.range,
    };
    const result = type === 'all' ? await searchAll(options) : await search(type, options);
    console.log(JSON.stringify(result, null, 2));
    return;
  }

  if (command === 'library') {
    const result = await getLibrary({
      page: flags.page,
      limit: flags.limit,
      q: flags.q,
      sortAsc: Boolean(flags.sortAsc || flags.sort),
    });
    console.log(JSON.stringify(result, null, 2));
    return;
  }

  if (command === 'song') {
    if (!positional[1]) throw new Error('songId is required');
    console.log(JSON.stringify(await getSongDetails(positional[1]), null, 2));
    return;
  }

  if (command === 'job') {
    if (!positional[1]) throw new Error('jobId is required');
    console.log(JSON.stringify(await getSongJob(positional[1]), null, 2));
    return;
  }

  if (command === 'wait') {
    if (!positional[1]) throw new Error('jobId is required');
    const result = await waitForJob(positional[1], {
      timeoutMs: flags.timeout ? Number(flags.timeout) : undefined,
      intervalMs: flags.interval ? Number(flags.interval) : undefined,
      onProgress: (job) => {
        process.stderr.write(`status: ${job.status || 'unknown'}${job.title ? ` - ${job.title}` : ''}\n`);
      },
    });
    console.log(JSON.stringify(result, null, 2));
    return;
  }

  throw new Error(`Unknown command "${command}". Run with --help for usage.`);
}

if (require.main === module) {
  main().catch((error) => {
    console.error(error.message);
    if (error.data) console.error(JSON.stringify(error.data, null, 2));
    process.exit(1);
  });
}

module.exports = {
  search,
  searchAll,
  getLibrary,
  getSongDetails,
  getSongJob,
  waitForJob,
  getUserInfo,
  generateLyrics,
  createSongFromDescription,
  createSongFromLyrics,
  getSessionSummary,
  GENRES,
  MOODS,
  VOCAL_GENDERS,
  RECORD_TYPES,
};
