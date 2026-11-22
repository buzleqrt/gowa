// credit© gist.vyr.my.id

import axios from "axios";
import fs from "fs";
import path from "path";

const _base = "https://imageupscaler.com";
const _ajax = `${_base}/wp-admin/admin-ajax.php`;

const _hdrs = (extra = {}) => ({
  "accept": "*/*",
  "accept-language": "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7",
  "cache-control": "no-cache",
  "pragma": "no-cache",
  "priority": "u=1, i",
  "sec-ch-ua": "\"Mises\";v=\"141\", \"Not?A_Brand\";v=\"8\", \"Chromium\";v=\"141\"",
  "sec-ch-ua-mobile": "?1",
  "sec-ch-ua-platform": "\"Android\"",
  "sec-fetch-dest": "empty",
  "sec-fetch-mode": "cors",
  "sec-fetch-site": "same-origin",
  "user-agent": "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
  ...extra,
});

const _session = async () => {
  const res = await axios.get(`${_base}/upscale-image-4x/`, {
    headers: _hdrs({ referer: _base }),
  });
  const m = res.data.match(/name="process_nonce"\s+value="([^"]+)"/);
  if (!m) throw new Error("nonce not found");

  const pidMatch = res.data.match(/["\s]pid["']?\s*[:=]\s*["']?([0-9]{20,35})/);
  const pid = pidMatch ? pidMatch[1] : null;

  const ivMatch = res.data.match(/indexVisitor["']?\s*[:=]\s*([0-9]+)/);
  const indexVisitor = ivMatch ? ivMatch[1] : null;

  const cookies = res.headers["set-cookie"]?.map(c => c.split(";")[0]).join("; ") ?? "";
  return { nonce: m[1], cookies, pid, indexVisitor };
};

const _b64 = (p) => {
  const buf = fs.readFileSync(p);
  const ext = path.extname(p).slice(1).toLowerCase();
  const mime = { png: "image/png", webp: "image/webp" }[ext] ?? "image/jpeg";
  return `data:${mime};base64,${buf.toString("base64")}`;
};

const _pid = () => {
  const a = Date.now().toString();
  const b = Math.random().toString().slice(2).padEnd(20, "0");
  return (a + b).slice(0, 30);
};

const run = async (imgPath, scale, outPath) => {
  if (!fs.existsSync(imgPath)) throw new Error(`file not found: ${imgPath}`);

  const increase = (scale === "2" || scale === "2x") ? "2" : "4";
  const fname = path.basename(imgPath);
  const fileId = `${Date.now()}_${fname}`;

  const { nonce, cookies, pid: pagePid } = await _session();
  const pid = pagePid ?? _pid();

  const mediaData = JSON.stringify([{
    fileSrc: _b64(imgPath),
    fileName: fname,
    fileId,
  }]);

  const parameters = JSON.stringify({
    "upscale-type": "standard",
    increase,
    "save-format": "auto",
  });

  const body = new URLSearchParams({
    action: "processing_images_adv",
    nonce,
    pid,
    function: "upscale-image-4x",
    batch_number: "1",
    total_batches: "1",
    mediaData,
    parameters,
  });

  const { data } = await axios.post(_ajax, body.toString(), {
    headers: _hdrs({
      "content-type": "application/x-www-form-urlencoded",
      "referer": `${_base}/upscale-image-4x/`,
      ...(cookies ? { cookie: cookies } : {}),
    }),
  });

  if (!data.success || !data.data?.items?.length) {
    throw new Error("server: " + JSON.stringify(data));
  }

  const item = data.data.items[0];
  const resultUrl = item.url;

  if (outPath) {
    const { data: img } = await axios.get(resultUrl, { responseType: "arraybuffer" });
    fs.writeFileSync(outPath, img);
  }

  process.stdout.write(JSON.stringify({ upscaled: resultUrl, saved: outPath ? path.resolve(outPath) : null }, null, 2) + "\n");
};

const args = process.argv.slice(2);
if (!args[0]) {
  process.stderr.write("usage: node imageupscaler.mjs <image> [2x|4x] [output]\n");
  process.exit(1);
}

let [img, a2, a3] = args;
let scale = "4x", out;

if (a2 && ["2x","4x","2","4"].includes(a2)) {
  scale = a2; out = a3;
} else if (a2) {
  out = a2;
}

run(img, scale, out).catch((e) => {
  process.stderr.write(JSON.stringify({ error: e.message }, null, 2) + "\n");
  process.exit(1);
});