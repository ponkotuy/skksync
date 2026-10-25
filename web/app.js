"use strict";

const $ = (id) => document.getElementById(id);

const storage = {
  get(key) {
    try { return localStorage.getItem(key) ?? ""; } catch { return ""; }
  },
  set(key, value) {
    try { localStorage.setItem(key, value); } catch { /* 保存できなくても動作は続ける */ }
  },
};

// ログインで GitHub に遷移する間、ドロップされたファイルを IndexedDB に退避しておく
const pending = {
  open() {
    return new Promise((resolve, reject) => {
      const req = indexedDB.open("skksync", 1);
      req.onupgradeneeded = () => req.result.createObjectStore("pending");
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  },
  async run(mode, fn) {
    const db = await this.open();
    return new Promise((resolve, reject) => {
      const tx = db.transaction("pending", mode);
      const req = fn(tx.objectStore("pending"));
      tx.oncomplete = () => resolve(req.result);
      tx.onerror = () => reject(tx.error);
    });
  },
  save(files) {
    return this.run("readwrite", (s) => s.put(files, "files"));
  },
  async take() {
    const files = await this.run("readonly", (s) => s.get("files"));
    if (files) await this.run("readwrite", (s) => s.delete("files"));
    return files;
  },
};

let files = [];
let loggedIn = false;
// 古いリクエストの応答で表示を上書きしないための通し番号
let requestSeq = 0;
let previewTimer;

function setMessage(text, isError = false) {
  $("message").textContent = text;
  $("message").classList.toggle("error", isError);
}

// Gist の URL と辞書ファイルが揃っていればプレビューする。
// 入力が変わったらプレビューが終わるまで更新できないようにする
function schedulePreview(delay = 0) {
  $("sync").disabled = true;
  clearTimeout(previewTimer);
  if ($("gist").value.trim() === "" || files.length === 0) return;
  previewTimer = setTimeout(() => submit("preview"), delay);
}

function updateEucjpLink() {
  const gist = $("gist").value.trim();
  const link = $("dl-eucjp");
  link.hidden = gist === "";
  $("dl-eucjp-none").hidden = gist !== "";
  if (gist === "") return;
  const params = new URLSearchParams({ gist });
  const file = $("file").value.trim();
  if (file !== "") params.set("file", file);
  link.href = `${location.origin}/dl/euc-jp?${params}`;
  link.textContent = link.href;
}

function setFiles(list) {
  files = [...list];
  const ul = $("file-list");
  ul.replaceChildren(...files.map((f) => {
    const li = document.createElement("li");
    li.textContent = `${f.name} (${(f.size / 1024).toFixed(1)} KB)`;
    return li;
  }));
}

async function loadAccount() {
  const res = await fetch("/api/me");
  const { login } = await res.json();
  loggedIn = !!login;
  $("login-name").textContent = login ? `@${login}` : "";
  $("logout-form").hidden = !login;
}

async function login() {
  setMessage("GitHub にログインします…");
  try {
    await pending.save(files);
  } catch {
    // 退避できなければ戻ってきたあとにファイルを選び直してもらう
  }
  location.href = "/login";
}

function renderResult(r) {
  const rows = [
    ["Gist の見出し語", r.counts.gist],
    ["手元の辞書の見出し語", r.counts.local],
    ["マージ後の見出し語", r.counts.merged],
    ["SKK-JISYO.L と重複して除いた候補", r.counts.removed_by_l],
  ];
  $("counts").replaceChildren(...rows.map(([label, n]) => {
    const tr = document.createElement("tr");
    const th = document.createElement("th");
    const td = document.createElement("td");
    th.textContent = label;
    td.textContent = n.toLocaleString();
    tr.append(th, td);
    return tr;
  }));

  const more = (shown, total) => (total > shown ? `\n… ほか ${total - shown} 件` : "");
  const d = r.diff;
  $("added-summary").textContent = `追加 ${r.counts.added} 件`;
  $("added").textContent = (d.added ?? []).join("\n") + more(d.added?.length ?? 0, r.counts.added);
  $("changed-summary").textContent = `変更 ${r.counts.changed} 件`;
  $("changed").textContent = (d.changed ?? []).map((c) => `- ${c.before}\n+ ${c.after}`).join("\n") + more(d.changed?.length ?? 0, r.counts.changed);
  $("removed-summary").textContent = `削除 ${r.counts.removed} 件`;
  $("removed").textContent = (d.removed ?? []).join("\n") + more(d.removed?.length ?? 0, r.counts.removed);
  $("result").hidden = false;

  const utf8 = $("dl-utf8");
  utf8.hidden = !r.gist.raw_url;
  $("dl-utf8-none").hidden = !!r.gist.raw_url;
  utf8.href = r.gist.raw_url;
  utf8.textContent = r.gist.raw_url;
}

async function submit(endpoint) {
  const gist = $("gist").value.trim();
  if (gist === "") return setMessage("Gist の URL を入力してください", true);
  if (files.length === 0) return setMessage("手元の辞書ファイルを指定してください", true);
  if (endpoint === "sync" && !loggedIn) return login();

  const form = new FormData();
  form.set("gist", gist);
  form.set("file", $("file").value.trim());
  for (const f of files) form.append("dict", f);

  clearTimeout(previewTimer);
  const seq = ++requestSeq;
  $("sync").disabled = true;
  setMessage(endpoint === "sync" ? "更新しています…" : "マージしています…");
  try {
    const res = await fetch(`/api/${endpoint}`, { method: "POST", body: form });
    if (seq !== requestSeq) return;
    // ページを開いている間にログインが切れた場合
    if (endpoint === "sync" && res.status === 401) return login();
    const body = await res.json();
    if (seq !== requestSeq) return;
    if (!res.ok) {
      setMessage(body.error ?? `エラー (${res.status})`, true);
      return;
    }
    renderResult(body);
    if (endpoint === "sync") {
      setMessage(body.updated ? "Gist を更新しました" : "変更がないため更新しませんでした");
    } else if (body.unchanged) {
      setMessage("Gist はすでに最新です");
    } else {
      setMessage("内容を確認して「Gist を更新」を押してください");
      $("sync").disabled = false;
    }
  } catch (e) {
    if (seq === requestSeq) setMessage(`通信に失敗しました: ${e}`, true);
  }
}

// ログインから戻ってきたら、退避したファイルを復元して更新を続ける
async function resumePending() {
  let saved;
  try {
    saved = await pending.take();
  } catch {
    return;
  }
  if (!saved?.length) return;
  setFiles(saved);
  if (loggedIn) {
    await submit("sync");
  } else {
    setMessage("ログインしなかったため更新を中止しました");
  }
}

async function init() {
  for (const id of ["gist", "file"]) {
    $(id).value = storage.get(`skksync.${id}`);
    $(id).addEventListener("input", () => {
      storage.set(`skksync.${id}`, $(id).value.trim());
      updateEucjpLink();
      schedulePreview(500);
    });
  }
  updateEucjpLink();

  const dz = $("dropzone");
  dz.addEventListener("dragover", (e) => { e.preventDefault(); dz.classList.add("over"); });
  dz.addEventListener("dragleave", () => dz.classList.remove("over"));
  dz.addEventListener("drop", (e) => {
    e.preventDefault();
    dz.classList.remove("over");
    setFiles(e.dataTransfer.files);
    schedulePreview();
  });
  $("files").addEventListener("change", (e) => {
    setFiles(e.target.files);
    schedulePreview();
  });

  $("sync").addEventListener("click", () => submit("sync"));

  try {
    await loadAccount();
  } catch (e) {
    setMessage(`ログイン状態を取得できません: ${e}`, true);
  }
  await resumePending();
}

init();
