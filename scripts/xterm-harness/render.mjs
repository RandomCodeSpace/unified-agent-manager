import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import process from "node:process";

const args = new Map();
for (let index = 2; index < process.argv.length; index += 2) {
  args.set(process.argv[index], process.argv[index + 1]);
}
const source = args.get("--ansi");
const outputDir = args.get("--evidence-dir");
const title = args.get("--title") ?? "UAM xterm visual QA";
if (!source || !outputDir || !path.isAbsolute(outputDir)) {
  throw new Error("--ansi and absolute --evidence-dir are required");
}

const harnessDir = path.dirname(new URL(import.meta.url).pathname);
const htmlURL = pathToFileURL(path.join(harnessDir, "index.html")).href;
const chrome = process.env.UAM_CHROME_BIN ?? "google-chrome";
const profileDir = process.env.UAM_CHROME_PROFILE;
if (!profileDir || !path.isAbsolute(profileDir)) {
  throw new Error("absolute UAM_CHROME_PROFILE is required");
}

class CDP {
  constructor(url) {
    this.nextID = 1;
    this.pending = new Map();
    this.socket = new WebSocket(url);
    this.socket.onmessage = (event) => {
      const message = JSON.parse(event.data);
      if (message.id && this.pending.has(message.id)) {
        const { resolve, reject } = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) reject(new Error(JSON.stringify(message.error)));
        else resolve(message.result);
      }
    };
  }

  async ready() {
    if (this.socket.readyState === WebSocket.OPEN) return;
    await new Promise((resolve, reject) => {
      this.socket.onopen = resolve;
      this.socket.onerror = reject;
    });
  }

  async send(method, params = {}) {
    await this.ready();
    const id = this.nextID++;
    const result = new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
    this.socket.send(JSON.stringify({ id, method, params }));
    return result;
  }

  close() {
    this.socket.close();
  }
}

function browserEndpoint(child) {
  return new Promise((resolve, reject) => {
    let stderr = "";
    const timer = setTimeout(() => reject(new Error(`Chrome endpoint timeout: ${stderr}`)), 10000);
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
      const match = stderr.match(/DevTools listening on (ws:\/\/\S+)/u);
      if (match) {
        clearTimeout(timer);
        resolve(match[1]);
      }
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`Chrome exited before endpoint, code=${code}: ${stderr}`));
    });
  });
}

async function waitForHarness(cdp) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const state = await cdp.send("Runtime.evaluate", {
      expression: "document.readyState === 'complete' && typeof window.renderAnsi === 'function'",
      returnByValue: true
    });
    if (state.result.value === true) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("xterm harness did not become ready");
}

async function renderANSI(cdp, ansi, title) {
  const render = await cdp.send("Runtime.evaluate", {
    expression: `window.renderAnsi(${JSON.stringify(ansi.toString("base64"))}, ${JSON.stringify(title)})`,
    awaitPromise: true,
    returnByValue: true
  });
  if (render.exceptionDetails) throw new Error(JSON.stringify(render.exceptionDetails));
  const text = await cdp.send("Runtime.evaluate", {
    expression: "window.__terminalText",
    returnByValue: true
  });
  const capture = await cdp.send("Page.captureScreenshot", {
    format: "png",
    captureBeyondViewport: false
  });
  return {
    terminal: render.result.value,
    text: text.result.value,
    png: Buffer.from(capture.data, "base64")
  };
}

const child = spawn(chrome, [
  "--headless=new",
  "--no-sandbox",
  "--disable-dev-shm-usage",
  "--disable-background-networking",
  "--hide-scrollbars",
  "--remote-allow-origins=*",
  "--remote-debugging-port=0",
  `--user-data-dir=${profileDir}`,
  "--window-size=1280,900",
  "about:blank"
], { stdio: ["ignore", "ignore", "pipe"] });
let browser;
let page;
try {
  const endpoint = await browserEndpoint(child);
  browser = new CDP(endpoint);
  const version = await browser.send("Browser.getVersion");
  const target = await browser.send("Target.createTarget", { url: htmlURL });
  const listURL = new URL(endpoint);
  listURL.protocol = "http:";
  listURL.pathname = "/json/list";
  let pageInfo;
  for (let attempt = 0; attempt < 100 && !pageInfo; attempt += 1) {
    const targets = await fetch(listURL).then((response) => response.json());
    pageInfo = targets.find((item) => item.id === target.targetId);
    if (!pageInfo) await new Promise((resolve) => setTimeout(resolve, 50));
  }
  if (!pageInfo) throw new Error("Chrome page target not found");
  page = new CDP(pageInfo.webSocketDebuggerUrl);
  await page.send("Page.enable");
  await page.send("Runtime.enable");
  await page.send("Emulation.setDeviceMetricsOverride", {
    width: 1280,
    height: 900,
    deviceScaleFactor: 1,
    mobile: false
  });
  await waitForHarness(page);
  const layout = await page.send("Page.getLayoutMetrics");
  const viewport = {
    width: layout.cssLayoutViewport.clientWidth,
    height: layout.cssLayoutViewport.clientHeight
  };
  if (viewport.width !== 1280 || viewport.height !== 900) {
    throw new Error(`unexpected capture viewport ${viewport.width}x${viewport.height}`);
  }

  const ansi = await readFile(source);
  const selection = await renderANSI(page, ansi, `${title} - selection`);
  const detailsMarker = Buffer.from("effective: focused");
  const restoreMarker = Buffer.from("\u001b[32;H\u001b[H");
  const detailsAt = ansi.indexOf(detailsMarker);
  const restoreAt = ansi.indexOf(restoreMarker, detailsAt);
  if (detailsAt < 0 || restoreAt < 0) {
    throw new Error("real PTY stream lacks details/effective-profile state boundaries");
  }
  await page.send("Runtime.evaluate", { expression: "window.resetTerminal()" });
  const details = await renderANSI(page, ansi.subarray(0, restoreAt), `${title} - details`);
  const xtermPackage = JSON.parse(await readFile(path.join(harnessDir, "node_modules", "@xterm", "xterm", "package.json"), "utf8"));
  const metadata = {
    renderer: "@xterm/xterm",
    xtermVersion: xtermPackage.version,
    browser: version.product,
    protocol: version.protocolVersion,
    viewport,
    terminal: details.terminal,
    states: {
      selection: { png: "terminal-selection.png", text: "terminal-selection.txt" },
      details: { png: "terminal.png", text: "terminal.txt" }
    },
    source: path.resolve(source),
    ansiSHA256: createHash("sha256").update(ansi).digest("hex"),
    pngSHA256: createHash("sha256").update(details.png).digest("hex"),
    textSHA256: createHash("sha256").update(details.text).digest("hex"),
    selectionPNG_SHA256: createHash("sha256").update(selection.png).digest("hex")
  };
  await writeFile(path.join(outputDir, "terminal-selection.png"), selection.png);
  await writeFile(path.join(outputDir, "terminal-selection.txt"), selection.text);
  await writeFile(path.join(outputDir, "terminal.png"), details.png);
  await writeFile(path.join(outputDir, "terminal.txt"), details.text);
  await writeFile(path.join(outputDir, "terminal-ansi.txt"), ansi);
  await writeFile(path.join(outputDir, "metadata.json"), `${JSON.stringify(metadata, null, 2)}\n`);
} finally {
  if (page) page.close();
  if (browser) browser.close();
  child.kill("SIGTERM");
  await new Promise((resolve) => {
    if (child.exitCode !== null) resolve();
    else child.once("exit", resolve);
  });
}
