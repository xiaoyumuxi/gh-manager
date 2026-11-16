// AI 工作台：详情抽屉、Agent 任务、发布 / 推送确认、AI 操作与设置管理。
// 依赖 app.js 中的全局状态与工具函数（app、dom、h、icon、api、toast 等）。
// 所有写回 GitHub 的动作都必须经过确认对话框，并在请求中携带 confirm: true。

const wb = {
  env: null,
  drawer: {
    itemID: null,
    tab: "overview",
    detail: null,
    detailError: "",
    detailLoading: false,
    runs: [],
    runsLoading: false,
    runID: null,
    run: null,
    runError: "",
    events: [],
    lastSeq: 0,
    source: null,
    resultView: "rendered",
    draft: "",
    els: {},
  },
  skills: new Map(),
  knownStatus: new Map(),
  runFetchTimers: new Map(),
  drawerRunsTimer: null,
  stateRefreshTimer: null,
  runStream: null,
  runContext: null,
  publishContext: null,
  applyContext: null,
  editingActionID: null,
  orgRepos: [],
  orgManaged: new Set(),
  orgSelected: new Set(),
};

const ACTIVE_STATUSES = new Set(["queued", "preparing", "running"]);

const RUN_STATUS = {
  queued: ["排队中", "pending"],
  preparing: ["准备中", "pending"],
  running: ["运行中", "running"],
  succeeded: ["已完成", "success"],
  failed: ["失败", "danger"],
  canceled: ["已取消", "muted"],
};

const ICONS = {
  play: '<path d="M6.5 4.5v11l9-5.5-9-5.5Z" fill="currentColor"/>',
  close:
    '<path d="m5 5 10 10M15 5 5 15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>',
  refresh:
    '<path d="M15.8 8A6 6 0 1 0 16 11.6M15.8 8V3.8M15.8 8h-4.2" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>',
  external:
    '<path d="M8 4.5H5.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V12M11 4.5h4.5V9M15.5 4.5 9 11" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  back: '<path d="M12 4.5 6.5 10l5.5 5.5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>',
  copy: '<rect x="7" y="7" width="9" height="9" rx="1.6" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M13 7V5.5A1.5 1.5 0 0 0 11.5 4h-6A1.5 1.5 0 0 0 4 5.5v6A1.5 1.5 0 0 0 5.5 13H7" fill="none" stroke="currentColor" stroke-width="1.5"/>',
  send: '<path d="M3.5 10 16.5 4l-4.6 12.5-2.3-5.1L3.5 10Z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>',
  push: '<path d="M10 16V6M5.5 10.5 10 6l4.5 4.5M4 3.5h12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  stop: '<rect x="5.5" y="5.5" width="9" height="9" rx="1.5" fill="currentColor"/>',
  trash:
    '<path d="M4.5 6h11M8 6V4.5h4V6M6 6l.7 9.5h6.6L14 6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>',
  diff: '<path d="M5 6.5h5M7.5 4v5M5 14h5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/><path d="M12.5 3.5h4v13h-4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/>',
  terminal:
    '<rect x="3" y="4" width="14" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="m6.5 8 2.5 2-2.5 2M10.5 12.5h3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>',
  undo: '<path d="M7 5 3.5 8.5 7 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/><path d="M4 8.5h7.5a4.5 4.5 0 0 1 0 9H9" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>',
};

/* ---------- 通用工具 ---------- */

function runStatusMeta(status) {
  const [label, tone] = RUN_STATUS[status] || [status || "未知", "muted"];
  return { label, tone };
}

function isActiveRun(run) {
  return Boolean(run) && ACTIVE_STATUSES.has(run.status);
}

function agentLabel(agent) {
  return agent === "codex" ? "Codex" : "Claude";
}

function modeLabel(mode) {
  return mode === "edit" ? "可修改代码" : "只读";
}

function appliesLabel(value) {
  if (value === "pr") return "仅 PR";
  if (value === "issue") return "仅 Issue";
  return "PR 与 Issue";
}

function itemByID(id) {
  const numeric = Number(id);
  return app.items.find((item) => item.id === numeric) || null;
}

function formatDuration(ms) {
  if (!ms || ms < 0) return "";
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  if (minutes < 60) return rest ? `${minutes} 分 ${rest} 秒` : `${minutes} 分钟`;
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`;
}

function runDuration(run) {
  const start = parseDate(run.started_at);
  if (!start) return run.duration_ms ? formatDuration(run.duration_ms) : "";
  const end = parseDate(run.finished_at) || new Date();
  return formatDuration(end.getTime() - start.getTime());
}

function runStatusChip(run) {
  const meta = runStatusMeta(run.status);
  return h(
    "span",
    { class: `run-status tone-${meta.tone}` },
    h("span", { class: "run-status-dot" }),
    h("span", { text: meta.label }),
  );
}

function runFlags(run) {
  const flags = [];
  if (run.applied_at) flags.push(["已推送", "success"]);
  if (run.published_at) flags.push(["已发布", "success"]);
  if (run.status === "succeeded" && run.has_changes && !run.applied_at && !run.discarded) {
    flags.push(["有改动", "warn"]);
  }
  if (run.discarded && run.has_changes) flags.push(["改动已丢弃", "muted"]);
  return flags;
}

function iconButton(iconPath, label, onclick, extra = {}) {
  return h(
    "button",
    { class: "icon-button", type: "button", title: label, "aria-label": label, onclick, ...extra },
    icon(iconPath),
  );
}

function actionButton(label, iconPath, onclick, { tone = "quiet", disabled = false, title = "" } = {}) {
  return h(
    "button",
    { class: `button button-${tone} button-small`, type: "button", onclick, disabled, title },
    iconPath ? icon(iconPath) : null,
    h("span", { text: label }),
  );
}

async function copyText(text, message = "已复制到剪贴板") {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const area = h("textarea", { value: text, style: { position: "fixed", opacity: "0" } });
    document.body.append(area);
    area.select();
    document.execCommand("copy");
    area.remove();
  }
  toast(message, "success");
}

function confirmAction({ title, message, confirmLabel = "确定", danger = false }) {
  return new Promise((resolve) => {
    dom.confirmTitle.textContent = title;
    dom.confirmMessage.textContent = message;
    dom.confirmOK.textContent = confirmLabel;
    dom.confirmOK.className = `button ${danger ? "button-danger" : "button-primary"}`;
    dom.confirmDialog.returnValue = "";
    const onClose = () => {
      dom.confirmDialog.removeEventListener("close", onClose);
      resolve(dom.confirmDialog.returnValue === "ok");
    };
    dom.confirmDialog.addEventListener("close", onClose);
    dom.confirmDialog.showModal();
    dom.confirmCancel.focus();
  });
}

/* ---------- Markdown 与 GitHub HTML 渲染（只构造 DOM，不使用 innerHTML） ---------- */

const LIST_RE = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const TABLE_DIVIDER_RE = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;
const INLINE_RE =
  /(`+)([\s\S]*?[^`])\1(?!`)|!?\[([^\]\n]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)|<(https?:\/\/[^>\s]+)>|(https?:\/\/[^\s<>()]*[^\s<>().,;:!?'"，。；：！？）])|\*\*([^*\n][\s\S]*?)\*\*|__([^_\n][\s\S]*?)__|~~([\s\S]+?)~~|\*([^*\s][^*\n]*?)\*|(?<![\w])_([^_\s][^_\n]*?)_(?![\w])/g;

function safeLink(href) {
  if (!href) return "";
  try {
    const url = new URL(href, "https://github.com/");
    if (url.protocol === "http:" || url.protocol === "https:" || url.protocol === "mailto:") {
      return url.href;
    }
  } catch {
    return "";
  }
  return "";
}

function markdownLink(href, children) {
  const url = safeLink(href);
  if (!url || !/^(https?:|mailto:)/i.test(href)) return h("span", {}, children);
  return h("a", { href: url, target: "_blank", rel: "noopener noreferrer" }, children);
}

function inlineMarkdown(text) {
  const nodes = [];
  let last = 0;
  for (const match of String(text).matchAll(INLINE_RE)) {
    if (match.index > last) nodes.push(document.createTextNode(text.slice(last, match.index)));
    if (match[1]) nodes.push(h("code", { text: match[2] }));
    else if (match[3]) nodes.push(markdownLink(match[4], inlineMarkdown(match[3])));
    else if (match[5]) nodes.push(markdownLink(match[5], [match[5]]));
    else if (match[6]) nodes.push(markdownLink(match[6], [match[6]]));
    else if (match[7] || match[8]) nodes.push(h("strong", {}, inlineMarkdown(match[7] || match[8])));
    else if (match[9]) nodes.push(h("del", {}, inlineMarkdown(match[9])));
    else if (match[10] || match[11]) nodes.push(h("em", {}, inlineMarkdown(match[10] || match[11])));
    last = match.index + match[0].length;
  }
  if (last < text.length) nodes.push(document.createTextNode(text.slice(last)));
  return nodes;
}

function inlineLines(lines) {
  const nodes = [];
  lines.forEach((line, index) => {
    if (index) nodes.push(h("br"));
    nodes.push(...inlineMarkdown(line));
  });
  return nodes;
}

function isFence(line) {
  return /^\s{0,3}(`{3,}|~{3,})/.test(line);
}

function isBlockStart(line, next) {
  return (
    isFence(line) ||
    /^\s{0,3}#{1,6}\s/.test(line) ||
    /^\s{0,3}>/.test(line) ||
    /^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line) ||
    LIST_RE.test(line) ||
    (line.includes("|") && next != null && TABLE_DIVIDER_RE.test(next))
  );
}

function splitTableRow(line) {
  let text = line.trim();
  if (text.startsWith("|")) text = text.slice(1);
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.slice(0, -1);
  return text.split(/(?<!\\)\|/).map((cell) => cell.trim().replace(/\\\|/g, "|"));
}

function parseMarkdownList(lines, start) {
  const first = lines[start].match(LIST_RE);
  const indent = first[1].length;
  const ordered = /\d/.test(first[2]);
  const list = h(ordered ? "ol" : "ul");
  if (ordered) {
    const number = parseInt(first[2], 10);
    if (number !== 1) list.start = number;
  }
  let index = start;
  while (index < lines.length) {
    const match = lines[index].match(LIST_RE);
    if (!match || match[1].length < indent) break;
    if (match[1].length > indent) {
      const [nested, next] = parseMarkdownList(lines, index);
      (list.lastElementChild || list).append(nested);
      index = next;
      continue;
    }
    if (/\d/.test(match[2]) !== ordered) break;
    const li = h("li");
    let text = match[3];
    const task = text.match(/^\[([ xX])\]\s+(.*)$/);
    if (task) {
      li.classList.add("task-item");
      li.append(h("input", { type: "checkbox", checked: task[1] !== " ", disabled: true }));
      text = task[2];
    }
    li.append(...inlineMarkdown(text));
    index += 1;
    while (
      index < lines.length &&
      lines[index].trim() &&
      !LIST_RE.test(lines[index]) &&
      /^\s+/.test(lines[index])
    ) {
      li.append(h("br"), ...inlineMarkdown(lines[index].trim()));
      index += 1;
    }
    list.append(li);
    if (index < lines.length && !lines[index].trim()) {
      let next = index;
      while (next < lines.length && !lines[next].trim()) next += 1;
      const nextMatch = next < lines.length && lines[next].match(LIST_RE);
      if (nextMatch && nextMatch[1].length >= indent) {
        index = next;
        continue;
      }
      break;
    }
  }
  return [list, index];
}

function renderMarkdownInto(root, source) {
  const lines = String(source || "").replace(/\r\n?/g, "\n").split("\n");
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      index += 1;
      continue;
    }
    const fence = line.match(/^\s{0,3}(`{3,}|~{3,})\s*([\w+#.-]*)/);
    if (fence) {
      const marker = fence[1];
      const body = [];
      index += 1;
      while (index < lines.length && !lines[index].trim().startsWith(marker)) {
        body.push(lines[index]);
        index += 1;
      }
      index += 1;
      root.append(
        h("pre", { dataset: fence[2] ? { lang: fence[2] } : {} }, h("code", { text: body.join("\n") })),
      );
      continue;
    }
    const heading = line.match(/^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/);
    if (heading) {
      root.append(h(`h${heading[1].length}`, {}, inlineMarkdown(heading[2])));
      index += 1;
      continue;
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      root.append(h("hr"));
      index += 1;
      continue;
    }
    if (/^\s{0,3}>/.test(line)) {
      const quote = [];
      while (index < lines.length && /^\s{0,3}>/.test(lines[index])) {
        quote.push(lines[index].replace(/^\s{0,3}>\s?/, ""));
        index += 1;
      }
      const block = h("blockquote");
      renderMarkdownInto(block, quote.join("\n"));
      root.append(block);
      continue;
    }
    if (line.includes("|") && index + 1 < lines.length && TABLE_DIVIDER_RE.test(lines[index + 1])) {
      const header = splitTableRow(line);
      const aligns = splitTableRow(lines[index + 1]).map((cell) => {
        if (cell.startsWith(":") && cell.endsWith(":")) return "center";
        if (cell.endsWith(":")) return "right";
        return "";
      });
      index += 2;
      const body = [];
      while (index < lines.length && lines[index].includes("|") && lines[index].trim()) {
        body.push(splitTableRow(lines[index]));
        index += 1;
      }
      const cell = (tag, text, column) =>
        h(tag, { style: aligns[column] ? { textAlign: aligns[column] } : {} }, inlineMarkdown(text));
      root.append(
        h(
          "div",
          { class: "table-wrap" },
          h(
            "table",
            {},
            h("thead", {}, h("tr", {}, header.map((text, column) => cell("th", text, column)))),
            h(
              "tbody",
              {},
              body.map((row) =>
                h("tr", {}, header.map((_, column) => cell("td", row[column] || "", column))),
              ),
            ),
          ),
        ),
      );
      continue;
    }
    if (LIST_RE.test(line)) {
      const [list, next] = parseMarkdownList(lines, index);
      root.append(list);
      index = next;
      continue;
    }
    const paragraph = [];
    do {
      paragraph.push(lines[index].trim());
      index += 1;
    } while (
      index < lines.length &&
      lines[index].trim() &&
      !isBlockStart(lines[index], lines[index + 1])
    );
    root.append(h("p", {}, inlineLines(paragraph)));
  }
  return root;
}

function renderMarkdown(source, className = "markdown") {
  return renderMarkdownInto(h("div", { class: className }), source);
}

const HTML_ALLOWED = new Set([
  "P", "BR", "HR", "H1", "H2", "H3", "H4", "H5", "H6", "UL", "OL", "LI", "BLOCKQUOTE", "PRE",
  "CODE", "EM", "STRONG", "B", "I", "DEL", "S", "INS", "SUB", "SUP", "A", "IMG", "TABLE", "THEAD",
  "TBODY", "TR", "TH", "TD", "DETAILS", "SUMMARY", "DIV", "SPAN", "KBD", "G-EMOJI", "INPUT", "DL",
  "DT", "DD", "SAMP", "VAR", "MARK", "SMALL",
]);

const HTML_DROPPED = new Set([
  "SCRIPT", "STYLE", "TEMPLATE", "IFRAME", "FRAME", "OBJECT", "EMBED", "SVG", "MATH", "FORM",
  "TEXTAREA", "SELECT", "BUTTON", "NOSCRIPT", "LINK", "META", "BASE", "VIDEO", "AUDIO", "CANVAS",
]);

function sanitizeInto(target, source) {
  for (const node of source.childNodes) {
    if (node.nodeType === Node.TEXT_NODE) {
      target.append(document.createTextNode(node.textContent));
      continue;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) continue;
    const tag = node.tagName.toUpperCase();
    if (HTML_DROPPED.has(tag)) continue;
    if (!HTML_ALLOWED.has(tag)) {
      sanitizeInto(target, node);
      continue;
    }
    const element = document.createElement(tag.toLowerCase());
    if (tag === "A") {
      const href = safeLink(node.getAttribute("href"));
      if (href) {
        element.href = href;
        element.target = "_blank";
        element.rel = "noopener noreferrer";
      }
    } else if (tag === "IMG") {
      const src = node.getAttribute("src") || "";
      if (!/^https:\/\//i.test(src)) continue;
      element.src = src;
      element.alt = node.getAttribute("alt") || "";
      element.loading = "lazy";
      element.referrerPolicy = "no-referrer";
      for (const attribute of ["width", "height"]) {
        const value = node.getAttribute(attribute);
        if (value && /^\d{1,4}$/.test(value)) element.setAttribute(attribute, value);
      }
    } else if (tag === "INPUT") {
      if ((node.getAttribute("type") || "").toLowerCase() !== "checkbox") continue;
      element.type = "checkbox";
      element.disabled = true;
      element.checked = node.hasAttribute("checked");
    } else if (tag === "TD" || tag === "TH") {
      const align = (node.getAttribute("align") || "").toLowerCase();
      if (["left", "right", "center"].includes(align)) element.style.textAlign = align;
    } else if (tag === "OL") {
      const start = node.getAttribute("start");
      if (start && /^\d{1,9}$/.test(start)) element.setAttribute("start", start);
    } else if (tag === "DETAILS" && node.hasAttribute("open")) {
      element.open = true;
    }
    sanitizeInto(element, node);
    target.append(element);
  }
}

function githubBody(html, markdown) {
  const container = h("div", { class: "markdown github-body" });
  if (html && html.trim()) {
    const parsed = new DOMParser().parseFromString(String(html), "text/html");
    sanitizeInto(container, parsed.body);
  } else if (markdown && markdown.trim()) {
    renderMarkdownInto(container, markdown);
  } else {
    container.append(h("p", { class: "muted", text: "没有描述。" }));
  }
  return container;
}

function renderPatch(patch) {
  const pre = h("pre", { class: "diff-patch" });
  const fragment = document.createDocumentFragment();
  for (const line of String(patch || "").split("\n")) {
    let className = "";
    if (line.startsWith("diff --git")) className = "diff-file";
    else if (line.startsWith("+++") || line.startsWith("---")) className = "diff-meta";
    else if (line.startsWith("@@")) className = "diff-hunk";
    else if (line.startsWith("+")) className = "diff-add";
    else if (line.startsWith("-")) className = "diff-del";
    else if (/^(index |new file|deleted file|similarity|rename |old mode|new mode|Binary)/.test(line)) {
      className = "diff-meta";
    }
    fragment.append(h("span", { class: className ? `diff-line ${className}` : "diff-line", text: line || " " }));
  }
  pre.append(fragment);
  return pre;
}

function renderDiffResult(container, diff) {
  container.replaceChildren();
  if (!diff || (!diff.stat && !diff.patch)) {
    container.append(h("p", { class: "muted", text: "没有改动。" }));
    return;
  }
  if (diff.stat) container.append(h("pre", { class: "diff-stat", text: diff.stat }));
  if (diff.truncated) {
    container.append(
      h("p", { class: "form-hint warn-text", text: "改动较大，以下只显示前一部分；推送时会提交全部改动。" }),
    );
  }
  container.append(renderPatch(diff.patch));
}

/* ---------- 环境、视图与表格中的 AI 列 ---------- */

async function loadEnvironment(refresh = false) {
  try {
    wb.env = await api(`/api/environment${refresh ? "?refresh=1" : ""}`);
    app.viewer = wb.env.viewer || null;
  } catch (error) {
    wb.env = { clis: [], viewer_error: error.message };
  }
  renderAgentIndicator();
  // 账号决定“待我审查”等视图；首次加载工作区之前不必重绘表格。
  if (app.data) renderTable();
  else renderWorkbenchChrome();
  if (dom.settingsDialog.open) renderEnvironmentGrid();
}

function cliStatus(name) {
  return (wb.env?.clis || []).find((cli) => cli.name === name) || null;
}

function renderAgentIndicator() {
  for (const [name, dot] of [
    ["claude", dom.claudeDot],
    ["codex", dom.codexDot],
  ]) {
    const status = cliStatus(name);
    dot.className = `agent-dot${status ? (status.available ? " is-ok" : " is-missing") : ""}`;
  }
  const active = Object.values(app.latestRuns).filter(isActiveRun).length;
  dom.agentActive.hidden = active === 0;
  dom.agentActive.textContent = String(active);
  const lines = ["claude", "codex"].map((name) => {
    const status = cliStatus(name);
    if (!status) return `${agentLabel(name)}：检测中`;
    return status.available
      ? `${agentLabel(name)}：${status.version || "可用"}`
      : `${agentLabel(name)}：不可用 ${status.error || ""}`.trim();
  });
  if (active) lines.push(`${active} 个任务运行中`);
  lines.push("点击查看最近的 AI 任务");
  dom.agentIndicator.title = lines.join("\n");
}

function renderWorkbenchChrome() {
  if (!dom.viewTabs) return;
  const login = app.viewer?.login;
  for (const tab of dom.viewTabs.querySelectorAll("[data-view]")) {
    const view = tab.dataset.view;
    tab.classList.toggle("is-active", view === app.filters.view);
    const needsViewer = view && view !== "ai";
    tab.disabled = Boolean(needsViewer && !login);
    tab.title = tab.disabled ? "需要先在终端执行 gh auth login，再刷新页面" : "";
  }
  for (const count of dom.viewTabs.querySelectorAll("[data-count]")) {
    const view = count.dataset.count;
    const value = view ? app.items.filter((item) => matchesView(item, view)).length : app.items.length;
    count.textContent = value ? String(value) : "";
  }
  dom.viewerLabel.replaceChildren();
  if (login) {
    dom.viewerLabel.append(avatarImage(login, 18), h("span", { text: `@${login}` }));
    dom.viewerLabel.title = "当前 GitHub 账号（来自 gh auth）";
  } else if (wb.env?.viewer_error) {
    dom.viewerLabel.append(h("span", { class: "warn-text", text: "未登录 GitHub CLI" }));
    dom.viewerLabel.title = wb.env.viewer_error;
  }
  const selected = app.selected.size;
  dom.batchBar.hidden = selected === 0;
  dom.batchCount.textContent = `已选 ${selected} 项`;
  renderAgentIndicator();
}

function aiCellContent(item) {
  const run = app.latestRuns[String(item.id)];
  if (!run) {
    return h(
      "button",
      {
        class: "ai-run-button",
        type: "button",
        title: "对该条目运行 AI 操作",
        dataset: { action: "run-item", itemId: item.id },
      },
      icon(ICONS.play),
      h("span", { text: "运行" }),
    );
  }
  const meta = runStatusMeta(run.status);
  const flag = runFlags(run)[0];
  return h(
    "button",
    {
      class: `ai-cell tone-${flag && !isActiveRun(run) ? flag[1] : meta.tone}`,
      type: "button",
      title: `${run.action_name} · ${agentLabel(run.agent)}\n${meta.label} · ${formatDateTime(run.created_at)}`,
      dataset: { action: "open-run", itemId: item.id, runId: run.id },
    },
    h("span", { class: "run-status-dot" }),
    h(
      "span",
      { class: "ai-cell-copy" },
      h("span", { class: "ai-cell-status", text: flag && !isActiveRun(run) ? flag[0] : meta.label }),
      h("small", { text: run.action_name }),
    ),
  );
}

function updateAICell(itemID) {
  if (app.filters.view === "ai" || app.sort.key === "ai") {
    renderTable();
    return;
  }
  const item = itemByID(itemID);
  const cell = dom.tableBody.querySelector(`tr[data-item-id="${Number(itemID)}"] td.col-ai`);
  if (item && cell) cell.replaceChildren(aiCellContent(item));
  renderWorkbenchChrome();
}

/* ---------- 任务状态推送 ---------- */

function connectRunStream() {
  if (wb.runStream) wb.runStream.close();
  const source = new EventSource("/api/runs/stream");
  wb.runStream = source;
  let opened = false;
  source.onopen = () => {
    // 断线重连后补齐期间错过的变化。
    if (opened) scheduleStateRefresh();
    opened = true;
  };
  source.onmessage = (message) => {
    let change;
    try {
      change = JSON.parse(message.data);
    } catch {
      return;
    }
    handleRunChange(change);
  };
  source.onerror = () => {
    if (source.readyState !== EventSource.CLOSED || wb.runStream !== source) return;
    wb.runStream = null;
    setTimeout(() => {
      if (!wb.runStream) connectRunStream();
    }, 5000);
  };
}

function scheduleStateRefresh() {
  clearTimeout(wb.stateRefreshTimer);
  wb.stateRefreshTimer = setTimeout(() => loadState({ silent: true }), 600);
}

function handleRunChange(change) {
  if (!change || !change.run_id) return;
  const previous = wb.knownStatus.get(change.run_id);
  wb.knownStatus.set(change.run_id, change.status);
  if (change.status === "deleted") {
    const latest = app.latestRuns[String(change.item_id)];
    if (latest && latest.id === change.run_id) {
      delete app.latestRuns[String(change.item_id)];
      updateAICell(change.item_id);
      scheduleStateRefresh();
    }
  } else {
    scheduleRunFetch(change.run_id, previous);
  }
  if (wb.drawer.itemID === change.item_id) scheduleDrawerRunsRefresh();
}

function scheduleRunFetch(runID, previousStatus) {
  clearTimeout(wb.runFetchTimers.get(runID));
  wb.runFetchTimers.set(
    runID,
    setTimeout(async () => {
      wb.runFetchTimers.delete(runID);
      let run;
      try {
        run = await api(`/api/runs/${runID}`);
      } catch {
        return;
      }
      const key = String(run.item_id);
      const latest = app.latestRuns[key];
      if (!latest || latest.id <= run.id) {
        app.latestRuns[key] = run;
        updateAICell(run.item_id);
      }
      if (previousStatus && ACTIVE_STATUSES.has(previousStatus) && !isActiveRun(run)) {
        const meta = runStatusMeta(run.status);
        toast(
          `${run.action_name}${meta.label}：${run.repo}#${run.number}`,
          run.status === "succeeded" ? "success" : "error",
        );
      }
      if (wb.drawer.runID === run.id) {
        wb.drawer.run = run;
        renderRunSummary();
      }
    }, 250),
  );
}

/* ---------- 详情抽屉 ---------- */

function setOpenRow(itemID) {
  app.openItemID = itemID;
  for (const row of dom.tableBody.querySelectorAll("tr.is-open")) row.classList.remove("is-open");
  if (itemID) {
    dom.tableBody.querySelector(`tr[data-item-id="${Number(itemID)}"]`)?.classList.add("is-open");
  }
}

function openItemDrawer(itemID, { tab = null, runID = null } = {}) {
  const item = itemByID(itemID);
  if (!item) {
    toast("该条目不在当前工作区中，可能已被移除");
    return;
  }
  const d = wb.drawer;
  const sameItem = d.itemID === item.id;
  if (!sameItem) {
    closeRunEvents();
    d.itemID = item.id;
    d.detail = null;
    d.detailError = "";
    d.runs = [];
    d.runID = null;
    d.run = null;
    d.draft = "";
  }
  if (tab) d.tab = tab;
  if (runID) {
    d.tab = "ai";
    selectRun(runID, { render: false });
  } else if (tab === "ai" && !sameItem) {
    d.runID = null;
  }
  dom.drawer.hidden = false;
  document.body.classList.add("has-drawer");
  setOpenRow(item.id);
  renderDrawer();
  if (!sameItem || !d.detail) loadDrawerDetail();
  loadDrawerRuns();
}

function closeDrawer() {
  closeRunEvents();
  dom.drawer.hidden = true;
  document.body.classList.remove("has-drawer");
  wb.drawer.itemID = null;
  wb.drawer.runID = null;
  wb.drawer.run = null;
  setOpenRow(null);
}

async function loadDrawerDetail(refresh = false) {
  const d = wb.drawer;
  const itemID = d.itemID;
  if (!itemID) return;
  d.detailLoading = true;
  d.detailError = "";
  if (d.tab !== "ai") renderDrawerBody();
  renderDrawerHeader();
  try {
    const detail = await api(`/api/items/${itemID}/detail${refresh ? "?refresh=1" : ""}`);
    if (d.itemID !== itemID) return;
    d.detail = detail;
  } catch (error) {
    if (d.itemID !== itemID) return;
    d.detailError = error.message;
  } finally {
    if (d.itemID === itemID) {
      d.detailLoading = false;
      renderDrawerHeader();
      renderDrawerTabs();
      if (d.tab !== "ai") renderDrawerBody();
    }
  }
}

async function loadDrawerRuns() {
  const d = wb.drawer;
  const itemID = d.itemID;
  if (!itemID) return;
  d.runsLoading = true;
  try {
    const runs = await api(`/api/runs?item_id=${itemID}&limit=50`);
    if (d.itemID !== itemID) return;
    d.runs = Array.isArray(runs) ? runs : [];
    for (const run of d.runs) {
      if (!wb.knownStatus.has(run.id)) wb.knownStatus.set(run.id, run.status);
    }
  } catch (error) {
    if (d.itemID === itemID) toast(`读取 AI 任务失败：${error.message}`);
  } finally {
    if (d.itemID === itemID) {
      d.runsLoading = false;
      renderDrawerTabs();
      if (d.tab === "ai" && !d.runID) renderDrawerBody();
    }
  }
}

function scheduleDrawerRunsRefresh() {
  clearTimeout(wb.drawerRunsTimer);
  wb.drawerRunsTimer = setTimeout(loadDrawerRuns, 300);
}

function renderDrawer() {
  renderDrawerHeader();
  renderDrawerTabs();
  renderDrawerBody();
}

function renderDrawerHeader() {
  const d = wb.drawer;
  const item = itemByID(d.itemID);
  if (!item) return;
  const detail = d.detail;
  const state = detail?.state || item.state;
  dom.drawerHeader.replaceChildren(
    h(
      "div",
      { class: "drawer-heading" },
      h(
        "div",
        { class: "drawer-kicker" },
        h("span", {
          class: `badge ${item.kind === "pr" ? "badge-pr" : "badge-issue"}`,
          text: item.kind === "pr" ? "Pull Request" : "Issue",
        }),
        h("span", { class: `badge badge-${state === "open" ? "open" : state}`, text: stateLabel(state) }),
        item.is_draft ? h("span", { class: "badge badge-draft", text: "草稿" }) : null,
        h("span", { class: "drawer-repo", text: `${item.repo}#${item.number}` }),
      ),
      h("h2", { class: "drawer-title", text: detail?.title || item.title }),
    ),
    h(
      "div",
      { class: "drawer-actions" },
      actionButton("运行 AI", ICONS.play, () => openRunDialog([item.id]), { tone: "primary" }),
      h(
        "a",
        {
          class: "icon-button",
          href: safeURL(item.url),
          target: "_blank",
          rel: "noopener noreferrer",
          title: "在 GitHub 中打开",
          "aria-label": "在 GitHub 中打开",
        },
        icon(ICONS.external),
      ),
      iconButton(ICONS.refresh, "从 GitHub 重新读取", () => {
        loadDrawerDetail(true);
        loadDrawerRuns();
      }, { disabled: d.detailLoading, class: `icon-button${d.detailLoading ? " is-spinning" : ""}` }),
      iconButton(ICONS.close, "关闭（Esc）", closeDrawer),
    ),
  );
}

function renderDrawerTabs() {
  const d = wb.drawer;
  const detail = d.detail;
  const item = itemByID(d.itemID);
  const discussionCount = detail
    ? (detail.comment_count || detail.comments?.length || 0) +
      (detail.reviews || []).filter((review) => review.body).length
    : item?.comments || 0;
  const tabs = [
    ["overview", "概览", null],
    ["discussion", "讨论", discussionCount || null],
    ["ai", "AI 任务", d.runs.length || null],
  ];
  dom.drawerTabs.replaceChildren(
    ...tabs.map(([key, label, count]) =>
      h(
        "button",
        {
          type: "button",
          class: `drawer-tab${d.tab === key ? " is-active" : ""}`,
          onclick: () => {
            if (d.tab === key && key === "ai" && d.runID) {
              backToRunList();
              return;
            }
            d.tab = key;
            renderDrawerTabs();
            renderDrawerBody();
          },
        },
        label,
        count ? h("span", { class: "view-count", text: String(count) }) : null,
      ),
    ),
  );
}

function renderDrawerBody() {
  const d = wb.drawer;
  const body = dom.drawerBody;
  if (d.tab !== "ai" || !d.runID) {
    closeRunEvents();
    d.els = {};
  }
  if (d.tab === "ai") {
    body.replaceChildren(d.runID ? renderRunView() : renderRunList());
    if (d.runID) {
      ensureRunEvents();
      requestAnimationFrame(() => scrollLogToEnd(true));
    }
    return;
  }
  if (d.detailLoading && !d.detail) {
    body.replaceChildren(h("div", { class: "drawer-loading", text: "正在从 GitHub 读取详情…" }));
    return;
  }
  if (d.detailError && !d.detail) {
    body.replaceChildren(
      h(
        "div",
        { class: "drawer-error" },
        h("strong", { text: "读取详情失败" }),
        h("p", { text: d.detailError }),
        actionButton("重试", ICONS.refresh, () => loadDrawerDetail(true)),
      ),
    );
    return;
  }
  body.replaceChildren(d.tab === "discussion" ? renderDiscussion() : renderOverview());
}

function metaRow(label, ...value) {
  return h("div", { class: "meta-row" }, h("span", { class: "meta-label", text: label }), h("div", { class: "meta-value" }, value));
}

function checkTone(state) {
  const value = String(state || "").toUpperCase();
  if (["SUCCESS", "NEUTRAL", "SKIPPED"].includes(value)) return "success";
  if (["FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"].includes(value)) {
    return "danger";
  }
  return "warn";
}

function renderOverview() {
  const d = wb.drawer;
  const item = itemByID(d.itemID);
  const detail = d.detail || {};
  const container = h("div", { class: "drawer-section-stack" });
  const meta = h("div", { class: "meta-grid" });
  meta.append(metaRow("作者", person(detail.author?.login || item.author)));
  meta.append(
    metaRow(
      "时间",
      h("span", {
        text: `创建于 ${formatDateTime(detail.created_at || item.created_at)} · 更新于 ${formatRelative(
          detail.updated_at || item.updated_at,
        )}`,
      }),
    ),
  );
  if (item.kind === "pr") {
    const head = detail.head_ref || item.pr?.head_ref;
    const base = detail.base_ref || item.pr?.base_ref;
    const headRepo = detail.head_repo || item.pr?.head_repo;
    const cross = detail.is_cross_repository ?? item.pr?.is_cross_repository;
    if (head || base) {
      meta.append(
        metaRow(
          "分支",
          h("code", { text: base || "?" }),
          h("span", { class: "muted", text: " ← " }),
          h("code", { text: cross && headRepo ? `${headRepo}:${head}` : head || "?" }),
        ),
      );
    }
    const additions = detail.additions ?? item.pr?.additions;
    const deletions = detail.deletions ?? item.pr?.deletions;
    const files = detail.changed_files ?? item.pr?.changed_files;
    if (additions != null) {
      meta.append(
        metaRow(
          "改动",
          h("span", { class: "diff-add-text", text: `+${additions}` }),
          h("span", { class: "diff-del-text", text: ` −${deletions}` }),
          h("span", { class: "muted", text: ` · ${files} 个文件` }),
        ),
      );
    }
    const review = reviewLabel(detail.review_decision || item.review_decision);
    const statusChips = [];
    if (review) statusChips.push(h("span", { class: `status-chip tone-${review[1]}`, text: review[0] }));
    const ci = ciLabel(detail.check_state || item.ci_status);
    if (ci) statusChips.push(h("span", { class: `status-chip tone-${ci[1]}`, text: ci[0] }));
    const mergeable = detail.mergeable || item.mergeable;
    if (mergeable === "CONFLICTING") statusChips.push(h("span", { class: "status-chip tone-danger", text: "有冲突" }));
    if (statusChips.length) meta.append(metaRow("状态", h("div", { class: "chip-row" }, statusChips)));
    if (item.reviewers?.length) {
      meta.append(metaRow("请求评审", h("div", { class: "assignee-list" }, item.reviewers.map((login) => assignee(login)))));
    }
  }
  const labels = currentLabels(item);
  if (labels.length) meta.append(metaRow("标签", h("div", { class: "chip-row wrap" }, labels.map(labelChip))));
  const assignees = currentAssignees(item);
  if (assignees.length) {
    meta.append(metaRow("指派", h("div", { class: "assignee-list" }, assignees.map((login) => assignee(login)))));
  }
  container.append(meta);

  container.append(
    h(
      "section",
      { class: "drawer-card" },
      h("div", { class: "drawer-card-title", text: "描述" }),
      githubBody(detail.body_html, detail.body ?? item.body),
    ),
  );

  if (item.kind === "pr" && Array.isArray(detail.checks) && detail.checks.length) {
    container.append(
      h(
        "details",
        { class: "drawer-card", open: detail.checks.some((check) => checkTone(check.conclusion || check.status) === "danger") },
        h("summary", { class: "drawer-card-title", text: `检查（${detail.checks.length}）` }),
        h(
          "ul",
          { class: "check-list" },
          detail.checks.map((check) => {
            const state = check.conclusion || check.status;
            return h(
              "li",
              {},
              h("span", { class: `run-status-dot tone-${checkTone(state)}` }),
              check.url
                ? h("a", { href: safeURL(check.url), target: "_blank", rel: "noopener noreferrer", text: check.name })
                : h("span", { text: check.name }),
              h("small", { class: "muted", text: String(state || "").toLowerCase() }),
            );
          }),
        ),
      ),
    );
  }

  if (item.kind === "pr" && Array.isArray(detail.files) && detail.files.length) {
    const more = (detail.file_count || detail.files.length) - detail.files.length;
    container.append(
      h(
        "details",
        { class: "drawer-card" },
        h("summary", { class: "drawer-card-title", text: `改动文件（${detail.file_count || detail.files.length}）` }),
        h(
          "ul",
          { class: "file-list" },
          detail.files.map((file) =>
            h(
              "li",
              {},
              h("code", { text: file.path, title: file.path }),
              h("span", { class: "diff-add-text", text: `+${file.additions}` }),
              h("span", { class: "diff-del-text", text: `−${file.deletions}` }),
            ),
          ),
          more > 0 ? h("li", { class: "muted", text: `还有 ${more} 个文件，请在 GitHub 中查看` }) : null,
        ),
      ),
    );
  }
  return container;
}

function reviewStateLabel(state) {
  const map = {
    APPROVED: ["批准", "success"],
    CHANGES_REQUESTED: ["请求修改", "danger"],
    COMMENTED: ["评审", "muted"],
    DISMISSED: ["已撤销", "muted"],
    PENDING: ["待提交", "warn"],
  };
  return map[state] || [state, "muted"];
}

function timelineEntry({ author, time, url, badge, html, markdown }) {
  return h(
    "article",
    { class: "timeline-entry" },
    h(
      "header",
      {},
      person(author?.login || "ghost"),
      badge ? h("span", { class: `status-chip tone-${badge[1]}`, text: badge[0] }) : null,
      h("span", { class: "muted", text: formatRelative(time), title: formatDateTime(time) }),
      h("span", { class: "toolbar-spacer" }),
      url
        ? h(
            "a",
            { class: "icon-button small", href: safeURL(url), target: "_blank", rel: "noopener noreferrer", title: "在 GitHub 中查看" },
            icon(ICONS.external),
          )
        : null,
    ),
    githubBody(html, markdown),
  );
}

function renderDiscussion() {
  const d = wb.drawer;
  const item = itemByID(d.itemID);
  const detail = d.detail || {};
  const container = h("div", { class: "drawer-section-stack" });
  const entries = [
    ...(detail.comments || []).map((comment) => ({ ...comment, time: comment.created_at, kind: "comment" })),
    ...(detail.reviews || [])
      .filter((review) => review.body || review.state === "APPROVED" || review.state === "CHANGES_REQUESTED")
      .map((review) => ({ ...review, time: review.submitted_at, kind: "review" })),
  ].sort((left, right) => (parseDate(left.time)?.getTime() || 0) - (parseDate(right.time)?.getTime() || 0));

  const total = detail.comment_count || 0;
  const shown = (detail.comments || []).length;
  if (total > shown) {
    container.append(
      h(
        "p",
        { class: "form-hint" },
        `共 ${total} 条评论，这里显示最近 ${shown} 条。`,
        h("a", { href: safeURL(item.url), target: "_blank", rel: "noopener noreferrer", text: "在 GitHub 中查看全部" }),
      ),
    );
  }
  if (!entries.length) {
    container.append(h("div", { class: "drawer-empty", text: "还没有评论。" }));
  }
  const timeline = h("div", { class: "timeline" });
  for (const entry of entries) {
    timeline.append(
      timelineEntry({
        author: entry.author,
        time: entry.time,
        url: entry.url,
        badge: entry.kind === "review" ? reviewStateLabel(entry.state) : null,
        html: entry.body_html,
        markdown: entry.body,
      }),
    );
  }
  container.append(timeline);

  const threads = (detail.review_threads || []).filter((thread) => !thread.is_resolved);
  if (threads.length) {
    container.append(h("div", { class: "dialog-section-title", text: `未解决的行内评审（${threads.length}）` }));
    for (const thread of threads) {
      container.append(
        h(
          "details",
          { class: "drawer-card thread" },
          h(
            "summary",
            {},
            h("code", { text: thread.line ? `${thread.path}:${thread.line}` : thread.path }),
            thread.is_outdated ? h("span", { class: "status-chip tone-muted", text: "代码已变更" }) : null,
            h("span", { class: "muted", text: `${thread.comments.length} 条` }),
          ),
          thread.comments.map((comment) =>
            timelineEntry({ author: comment.author, time: comment.created_at, url: comment.url, html: comment.body_html, markdown: comment.body }),
          ),
        ),
      );
    }
  }

  const textarea = h("textarea", {
    class: "composer-input",
    rows: 4,
    placeholder: item.kind === "pr" ? "写评论或评审意见（支持 Markdown）" : "写评论（支持 Markdown）",
    value: d.draft,
    oninput: (event) => {
      d.draft = event.target.value;
    },
  });
  container.append(
    h(
      "div",
      { class: "composer" },
      textarea,
      h(
        "div",
        { class: "composer-actions" },
        h("span", { class: "form-hint", text: "发布前会显示确认对话框。" }),
        actionButton("预览并发布…", ICONS.send, () => {
          if (!d.draft.trim()) {
            toast("请先填写内容");
            textarea.focus();
            return;
          }
          openPublishDialog({ item, body: d.draft, fromComposer: true });
        }, { tone: "primary" }),
      ),
    ),
  );
  return container;
}

/* ---------- AI 任务列表与详情 ---------- */

function renderRunList() {
  const d = wb.drawer;
  const item = itemByID(d.itemID);
  const container = h("div", { class: "drawer-section-stack" });
  const applicable = app.actions.filter((action) => action.applies_to === "all" || action.applies_to === item.kind);
  container.append(
    h(
      "div",
      { class: "quick-actions" },
      applicable.slice(0, 6).map((action) =>
        h(
          "button",
          {
            type: "button",
            class: "quick-action",
            title: `${agentLabel(action.agent)} · ${modeLabel(action.mode)}${action.skill ? ` · SKILL ${action.skill}` : ""}`,
            onclick: () => openRunDialog([item.id], { actionID: action.id }),
          },
          h("span", { class: `agent-badge agent-${action.agent}`, text: agentLabel(action.agent) }),
          h("span", { text: action.name }),
        ),
      ),
      h("button", { type: "button", class: "quick-action is-more", onclick: () => openRunDialog([item.id]) }, "更多选项…"),
    ),
  );
  if (d.runsLoading && !d.runs.length) {
    container.append(h("div", { class: "drawer-loading", text: "正在读取 AI 任务…" }));
    return container;
  }
  if (!d.runs.length) {
    container.append(h("div", { class: "drawer-empty", text: "还没有对该条目运行过 AI 操作。" }));
    return container;
  }
  const list = h("div", { class: "run-list" });
  for (const run of d.runs) {
    list.append(
      h(
        "button",
        { type: "button", class: "run-card", onclick: () => selectRun(run.id) },
        h(
          "div",
          { class: "run-card-main" },
          h("strong", { text: run.action_name }),
          h(
            "span",
            { class: "muted" },
            `${agentLabel(run.agent)} · ${modeLabel(run.mode)}${run.skill ? ` · ${run.skill}` : ""}`,
          ),
        ),
        h(
          "div",
          { class: "run-card-side" },
          h("div", { class: "chip-row" }, runStatusChip(run), runFlags(run).map(([label, tone]) => h("span", { class: `status-chip tone-${tone}`, text: label }))),
          h("small", { class: "muted", text: `#${run.id} · ${formatRelative(run.created_at)}${runDuration(run) ? ` · ${runDuration(run)}` : ""}` }),
        ),
      ),
    );
  }
  container.append(list);
  return container;
}

function selectRun(runID, { render = true } = {}) {
  const d = wb.drawer;
  if (d.runID !== runID) {
    closeRunEvents();
    d.events = [];
    d.lastSeq = 0;
    d.resultView = "rendered";
  }
  d.runID = runID;
  d.run = d.runs.find((run) => run.id === runID) || (d.run?.id === runID ? d.run : null);
  d.runError = "";
  d.tab = "ai";
  if (render) {
    renderDrawerTabs();
    renderDrawerBody();
  }
  fetchDrawerRun(runID);
}

function backToRunList() {
  const d = wb.drawer;
  closeRunEvents();
  d.runID = null;
  d.run = null;
  renderDrawerBody();
  loadDrawerRuns();
}

async function fetchDrawerRun(runID) {
  const d = wb.drawer;
  try {
    const run = await api(`/api/runs/${runID}`);
    if (d.runID !== runID) return;
    d.run = run;
    d.runError = "";
    if (!wb.knownStatus.has(run.id)) wb.knownStatus.set(run.id, run.status);
  } catch (error) {
    if (d.runID !== runID) return;
    d.runError = error.message;
  }
  renderRunSummary();
}

function renderRunView() {
  const d = wb.drawer;
  const summary = h("div", { class: "run-summary" });
  const log = h("div", { class: "run-log", role: "log", "aria-live": "off" });
  const logStatus = h("span", { class: "muted" });
  d.els = { summary, log, logStatus };
  for (const event of d.events) log.append(renderEvent(event));
  const container = h(
    "div",
    { class: "drawer-section-stack" },
    h(
      "div",
      { class: "run-nav" },
      h(
        "button",
        { type: "button", class: "link-button", onclick: backToRunList },
        icon(ICONS.back),
        "全部 AI 任务",
      ),
    ),
    summary,
    h(
      "details",
      { class: "drawer-card", open: true },
      h("summary", { class: "drawer-card-title" }, "执行日志", logStatus),
      log,
    ),
  );
  renderRunSummary();
  return container;
}

function renderRunSummary() {
  const d = wb.drawer;
  const summary = d.els.summary;
  if (!summary) return;
  const run = d.run;
  if (!run) {
    summary.replaceChildren(
      d.runError
        ? h("div", { class: "drawer-error" }, h("strong", { text: "读取任务失败" }), h("p", { text: d.runError }))
        : h("div", { class: "drawer-loading", text: "正在读取任务…" }),
    );
    return;
  }
  const active = isActiveRun(run);
  const terminal = !active;
  const canApply =
    terminal && run.status === "succeeded" && run.mode === "edit" && run.has_changes && !run.discarded && !run.applied_at;
  const actions = [];
  if (active) {
    actions.push(actionButton("取消任务", ICONS.stop, () => cancelRun(run), { tone: "danger" }));
  }
  if (run.status === "succeeded" && run.result) {
    actions.push(
      actionButton(run.published_at ? "再次发布…" : "发布到 GitHub…", ICONS.send, () => publishRun(run), {
        tone: canApply || run.published_at ? "quiet" : "primary",
      }),
    );
  }
  if (canApply) {
    actions.push(actionButton("推送改动…", ICONS.push, () => openApplyDialog(run), { tone: "primary" }));
  }
  if (terminal && run.mode === "edit" && run.has_changes && !run.discarded) {
    actions.push(actionButton("查看改动", ICONS.diff, () => openDiffDialog(run)));
  }
  if (canApply) {
    actions.push(actionButton("丢弃改动", ICONS.undo, () => discardRun(run)));
  }
  if (terminal && run.resume_command) {
    actions.push(
      actionButton("复制续聊命令", ICONS.terminal, () => copyText(run.resume_command, "已复制，在终端中粘贴即可继续对话"), {
        title: run.resume_command,
      }),
    );
  }
  if (terminal) {
    actions.push(actionButton("再次运行", ICONS.refresh, () => openRunDialog([run.item_id], { actionID: run.action_id })));
    actions.push(actionButton("删除记录", ICONS.trash, () => deleteRun(run)));
  }

  const details = [
    `${agentLabel(run.agent)} · ${modeLabel(run.mode)}`,
    run.skill ? `SKILL ${run.skill}` : "",
    run.model ? `模型 ${run.model}` : "",
    `创建于 ${formatDateTime(run.created_at)}`,
    runDuration(run) ? `耗时 ${runDuration(run)}` : "",
    run.cost_usd ? `费用 $${run.cost_usd.toFixed(run.cost_usd < 1 ? 3 : 2)}` : "",
  ].filter(Boolean);

  const children = [
    h(
      "div",
      { class: "run-header" },
      h(
        "div",
        {},
        h("h3", {}, `#${run.id} ${run.action_name}`),
        h("p", { class: "muted", text: details.join(" · ") }),
      ),
      h("div", { class: "chip-row" }, runStatusChip(run), runFlags(run).map(([label, tone]) => h("span", { class: `status-chip tone-${tone}`, text: label }))),
    ),
    actions.length ? h("div", { class: "run-actions" }, actions) : null,
  ];
  if (run.published_url) {
    children.push(
      h(
        "p",
        { class: "run-link" },
        `已于 ${formatDateTime(run.published_at)} 发布：`,
        h("a", { href: safeURL(run.published_url), target: "_blank", rel: "noopener noreferrer", text: run.published_url }),
      ),
    );
  }
  if (run.applied_url) {
    children.push(
      h(
        "p",
        { class: "run-link" },
        `已于 ${formatDateTime(run.applied_at)} 推送：`,
        h("a", { href: safeURL(run.applied_url), target: "_blank", rel: "noopener noreferrer", text: run.applied_url }),
      ),
    );
  }
  if (run.error) {
    children.push(h("div", { class: `run-error${run.status === "canceled" ? " is-muted" : ""}`, text: run.error }));
  }
  if (run.diff_stat && !run.discarded) {
    children.push(
      h(
        "section",
        { class: "drawer-card" },
        h("div", { class: "drawer-card-title", text: run.applied_at ? "已推送的改动" : "工作区改动" }),
        h("pre", { class: "diff-stat", text: run.diff_stat }),
      ),
    );
  }
  if (run.result) {
    const toggle = h(
      "div",
      { class: "segmented small" },
      ["rendered", "source"].map((view) =>
        h(
          "button",
          {
            type: "button",
            class: d.resultView === view ? "is-active" : "",
            onclick: () => {
              d.resultView = view;
              renderRunSummary();
            },
          },
          view === "rendered" ? "预览" : "源码",
        ),
      ),
    );
    children.push(
      h(
        "section",
        { class: "drawer-card result-card" },
        h(
          "div",
          { class: "drawer-card-title" },
          "结果",
          h("span", { class: "toolbar-spacer" }),
          toggle,
          iconButton(ICONS.copy, "复制结果", () => copyText(run.result), { class: "icon-button small" }),
        ),
        d.resultView === "source" ? h("pre", { class: "result-source", text: run.result }) : renderMarkdown(run.result),
      ),
    );
  } else if (active) {
    children.push(h("div", { class: "drawer-loading", text: "Agent 正在工作，结果会在完成后显示在这里。" }));
  }
  if (run.prompt) {
    children.push(
      h(
        "details",
        { class: "drawer-card" },
        h("summary", { class: "drawer-card-title", text: "发送给 Agent 的提示词" }),
        h("pre", { class: "result-source", text: run.prompt }),
      ),
    );
  }
  summary.replaceChildren(...children.filter(Boolean));
  if (d.els.logStatus) {
    d.els.logStatus.textContent = d.source ? " · 实时" : d.events.length ? ` · ${d.events.length} 条` : "";
  }
}

function eventLabel(event) {
  const map = {
    status: "状态",
    assistant: "回复",
    thinking: "思考",
    tool: event.tool || "工具",
    tool_result: "输出",
    tool_error: "工具错误",
    stderr: "stderr",
    error: "错误",
    state: "阶段",
    end: "结束",
  };
  return map[event.type] || event.type;
}

function renderEvent(event) {
  const time = event.t ? new Date(event.t) : null;
  const stamp = time ? `${pad(time.getHours())}:${pad(time.getMinutes())}:${pad(time.getSeconds())}` : "";
  let body;
  const text = String(event.text || "");
  if (event.type === "assistant") {
    body = renderMarkdown(text, "markdown compact");
  } else if (event.type === "state" || event.type === "end") {
    body = h("span", { text: runStatusMeta(text).label });
  } else if ((event.type === "tool_result" || event.type === "thinking") && text.length > 280) {
    body = h("details", {}, h("summary", { text: `${text.slice(0, 160).replace(/\s+/g, " ")}…` }), h("pre", { text }));
  } else if (event.type === "tool" || event.type === "tool_result" || event.type === "stderr") {
    body = h("pre", { text });
  } else {
    body = h("span", { class: "event-text", text });
  }
  return h(
    "div",
    { class: `log-entry log-${event.type}` },
    h("span", { class: "log-time", text: stamp }),
    h("span", { class: "log-type", text: eventLabel(event) }),
    h("div", { class: "log-body" }, body),
  );
}

function scrollLogToEnd(force = false) {
  const log = wb.drawer.els.log;
  if (!log) return;
  const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
  if (force || nearBottom) log.scrollTop = log.scrollHeight;
}

function ensureRunEvents() {
  const d = wb.drawer;
  if (d.source || !d.runID) return;
  const runID = d.runID;
  const source = new EventSource(`/api/runs/${runID}/events`);
  d.source = source;
  source.onmessage = (message) => {
    if (d.runID !== runID) return;
    let event;
    try {
      event = JSON.parse(message.data);
    } catch {
      return;
    }
    if (event.seq && event.seq <= d.lastSeq) return;
    if (event.seq) d.lastSeq = event.seq;
    d.events.push(event);
    if (d.els.log) {
      const log = d.els.log;
      const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
      log.append(renderEvent(event));
      if (nearBottom) log.scrollTop = log.scrollHeight;
    }
    if (event.type === "state" || event.type === "end") fetchDrawerRun(runID);
  };
  source.onerror = () => {
    // 任务不存在或服务端拒绝时 EventSource 不会重连。
    if (source.readyState === EventSource.CLOSED && d.source === source) {
      d.source = null;
      if (d.els.logStatus) d.els.logStatus.textContent = " · 已断开";
    }
  };
  source.addEventListener("done", () => {
    source.close();
    if (d.source === source) d.source = null;
    if (d.runID === runID) {
      if (d.els.logStatus) d.els.logStatus.textContent = d.events.length ? ` · ${d.events.length} 条` : "";
      if (!d.events.length && d.els.log) d.els.log.append(h("div", { class: "muted", text: "没有日志。" }));
    }
  });
  if (d.els.logStatus) d.els.logStatus.textContent = " · 连接中";
}

function closeRunEvents() {
  const d = wb.drawer;
  if (d.source) {
    d.source.close();
    d.source = null;
  }
}

async function refreshAfterRunMutation(run) {
  if (wb.drawer.runID === run.id) await fetchDrawerRun(run.id);
  scheduleDrawerRunsRefresh();
  scheduleRunFetch(run.id);
}

async function cancelRun(run) {
  try {
    await api(`/api/runs/${run.id}/cancel`, { method: "POST", body: {} });
    toast("已请求取消", "success");
  } catch (error) {
    toast(error.message);
  }
}

async function discardRun(run) {
  const ok = await confirmAction({
    title: "丢弃本地改动？",
    message: `将删除任务 #${run.id} 的工作目录，Agent 做的修改会全部丢失（GitHub 上不受影响）。`,
    confirmLabel: "丢弃改动",
    danger: true,
  });
  if (!ok) return;
  try {
    await api(`/api/runs/${run.id}/discard`, { method: "POST", body: {} });
    toast("改动已丢弃", "success");
    await refreshAfterRunMutation(run);
  } catch (error) {
    toast(error.message);
  }
}

async function deleteRun(run) {
  const ok = await confirmAction({
    title: "删除任务记录？",
    message: `将删除任务 #${run.id} 的记录、日志和本地工作目录。已发布到 GitHub 的内容不受影响。`,
    confirmLabel: "删除",
    danger: true,
  });
  if (!ok) return;
  try {
    await api(`/api/runs/${run.id}`, { method: "DELETE" });
    toast("任务已删除", "success");
    if (wb.drawer.runID === run.id) backToRunList();
    scheduleStateRefresh();
  } catch (error) {
    toast(error.message);
  }
}

/* ---------- 运行对话框 ---------- */

async function loadSkills(repo) {
  const key = repo || "";
  const cached = wb.skills.get(key);
  if (cached && Date.now() - cached.at < 5 * 60 * 1000) return cached;
  const query = repo ? `?repo=${encodeURIComponent(repo)}` : "";
  const response = await api(`/api/agent/skills${query}`);
  const entry = { skills: response.skills || [], error: response.repo_error || "", at: Date.now() };
  wb.skills.set(key, entry);
  return entry;
}

function fillSkillList(datalist, skills, agent) {
  const seen = new Set();
  const options = [];
  for (const skill of skills) {
    if (skill.agent !== "repo" && agent && skill.agent !== agent) continue;
    if (seen.has(skill.name)) continue;
    seen.add(skill.name);
    const origin = skill.agent === "repo" ? `仓库 ${skill.source}` : `本机 ${agentLabel(skill.agent)}`;
    const description = skill.description ? ` · ${skill.description.slice(0, 90)}` : "";
    options.push(h("option", { value: skill.name, label: `${origin}${description}` }));
  }
  datalist.replaceChildren(...options);
  return options.length;
}

function selectedRunAction() {
  return app.actions.find((action) => String(action.id) === dom.runAction.value) || null;
}

function openRunDialog(itemIDs, { actionID = null } = {}) {
  const items = [...new Set(itemIDs.map(Number))].map(itemByID).filter(Boolean);
  if (!items.length) return;
  if (!app.actions.length) {
    toast("还没有可用的 AI 操作，请先在“AI 操作”中创建");
    openActionsDialog();
    return;
  }
  wb.runContext = { items, skills: [], skillsError: "", skillsLoading: true };
  const kinds = new Set(items.map((item) => item.kind));
  const preview = items.slice(0, 6).map((item) =>
    h(
      "span",
      { class: "run-target", title: item.title },
      h("span", { class: `badge ${item.kind === "pr" ? "badge-pr" : "badge-issue"}`, text: item.kind === "pr" ? "PR" : "Issue" }),
      h("span", { text: `${item.repo}#${item.number}` }),
      h("span", { class: "muted", text: item.title }),
    ),
  );
  dom.runTargets.replaceChildren(
    h("div", { class: "run-target-title", text: `将对 ${items.length} 个条目运行` }),
    ...preview,
    ...(items.length > preview.length ? [h("span", { class: "muted", text: `…以及另外 ${items.length - preview.length} 个` })] : []),
  );

  const options = app.actions.map((action) => {
    const applicable = action.applies_to === "all" || kinds.has(action.applies_to);
    return h("option", {
      value: String(action.id),
      text: `${action.name}（${agentLabel(action.agent)} · ${modeLabel(action.mode)}）`,
      disabled: !applicable,
    });
  });
  dom.runAction.replaceChildren(...options);
  const kindKey = [...kinds].sort().join(",");
  const remembered = localStorage.getItem(`gh-manager:last-action:${kindKey}`);
  const candidates = [actionID, remembered].filter(Boolean).map(String);
  const usable = (id) => options.some((option) => option.value === id && !option.disabled);
  dom.runAction.value = candidates.find(usable) || options.find((option) => !option.disabled)?.value || "";
  dom.runExtra.value = "";
  syncRunActionDefaults();
  dom.runSubmit.disabled = false;
  dom.runSubmit.textContent = "开始运行";
  if (!dom.runDialog.open) dom.runDialog.showModal();

  const repos = [...new Set(items.map((item) => item.repo))];
  const repo = repos.length === 1 ? repos[0] : "";
  dom.runSkillHint.textContent = repo ? `正在读取 ${repo} 中的 SKILL…` : "选中了多个仓库，仅列出本机 SKILL；也可以直接输入仓库 SKILL 名称。";
  loadSkills(repo)
    .then((entry) => {
      if (!wb.runContext || wb.runContext.items !== items) return;
      wb.runContext.skills = entry.skills;
      wb.runContext.skillsError = entry.error;
      wb.runContext.skillsLoading = false;
      renderRunSkillHint(repo);
    })
    .catch((error) => {
      if (!wb.runContext || wb.runContext.items !== items) return;
      wb.runContext.skillsLoading = false;
      wb.runContext.skillsError = error.message;
      renderRunSkillHint(repo);
    });
}

function renderRunSkillHint(repo) {
  const context = wb.runContext;
  if (!context) return;
  const action = selectedRunAction();
  const count = fillSkillList(dom.runSkillList, context.skills, action?.agent);
  const repoSkills = context.skills.filter((skill) => skill.agent === "repo");
  const parts = [];
  if (context.skillsLoading) parts.push("正在读取 SKILL…");
  else if (repo && repoSkills.length) parts.push(`${repo} 提供 ${repoSkills.length} 个仓库 SKILL：${repoSkills.map((skill) => skill.name).join("、")}`);
  else if (repo) parts.push(`${repo} 的默认分支中没有 .agents/skills、.claude/skills 或 .codex/skills。`);
  if (!context.skillsLoading) parts.push(`可选 ${count} 个 SKILL，点击输入框查看。`);
  if (context.skillsError) parts.push(context.skillsError);
  dom.runSkillHint.textContent = parts.join(" ");
}

function syncRunActionDefaults() {
  const action = selectedRunAction();
  dom.runSkill.value = action?.skill || "";
  updateRunActionHint();
  if (wb.runContext && !wb.runContext.skillsLoading) {
    const repos = [...new Set(wb.runContext.items.map((item) => item.repo))];
    renderRunSkillHint(repos.length === 1 ? repos[0] : "");
  }
}

function updateRunActionHint() {
  const action = selectedRunAction();
  const context = wb.runContext;
  if (!action || !context) {
    dom.runActionHint.textContent = "";
    return;
  }
  const skipped = context.items.filter((item) => action.applies_to !== "all" && action.applies_to !== item.kind).length;
  const parts = [`${agentLabel(action.agent)} · ${modeLabel(action.mode)} · 适用于${appliesLabel(action.applies_to)}`];
  if (action.model) parts.push(`模型 ${action.model}`);
  if (skipped) parts.push(`将跳过 ${skipped} 个不适用的条目`);
  if (action.mode === "edit") {
    parts.push("Agent 会在独立工作区修改代码，推送前需要你确认");
  }
  const cli = cliStatus(action.agent);
  if (cli && !cli.available) parts.push(`⚠ 未检测到 ${agentLabel(action.agent)} CLI，请在设置中配置命令路径`);
  dom.runActionHint.textContent = parts.join("；");
  dom.runActionHint.classList.toggle("warn-text", Boolean(cli && !cli.available));
}

async function submitRunDialog(event) {
  event.preventDefault();
  const context = wb.runContext;
  const action = selectedRunAction();
  if (!context || !action) return;
  dom.runSubmit.disabled = true;
  dom.runSubmit.textContent = "正在启动…";
  try {
    const response = await api("/api/runs", {
      method: "POST",
      body: {
        item_ids: context.items.map((item) => item.id),
        action_id: action.id,
        extra_prompt: dom.runExtra.value,
        skill: dom.runSkill.value.trim(),
      },
    });
    const kindKey = [...new Set(context.items.map((item) => item.kind))].sort().join(",");
    localStorage.setItem(`gh-manager:last-action:${kindKey}`, String(action.id));
    const results = response.results || [];
    const started = results.filter((result) => result.run);
    const failed = results.filter((result) => result.error);
    for (const result of started) {
      app.latestRuns[String(result.item_id)] = result.run;
      wb.knownStatus.set(result.run.id, result.run.status);
    }
    dom.runDialog.close();
    if (context.items.length > 1) app.selected.clear();
    renderTable();
    if (failed.length) {
      toast(`${started.length} 个任务已启动，${failed.length} 个未启动：${failed[0].error}`);
    } else {
      toast(`已启动 ${started.length} 个任务`, "success");
    }
    if (context.items.length === 1 && started.length === 1) {
      openItemDrawer(context.items[0].id, { tab: "ai", runID: started[0].run.id });
    } else if (wb.drawer.itemID) {
      scheduleDrawerRunsRefresh();
    }
  } catch (error) {
    toast(error.message);
  } finally {
    dom.runSubmit.disabled = false;
    dom.runSubmit.textContent = "开始运行";
  }
}

/* ---------- 发布确认 ---------- */

function publishFooter(run) {
  const login = app.viewer?.login ? `@${app.viewer.login}` : "维护者";
  return `\n\n---\n<sub>🤖 由 ${agentLabel(run.agent)}（${run.action_name}）生成，经 ${login} 确认后通过 gh-manager 发布。</sub>`;
}

function publishRun(run) {
  const item = itemByID(run.item_id);
  if (!item) {
    toast("找不到对应的条目");
    return;
  }
  const body = run.published_at ? run.result : `${run.result.trim()}${publishFooter(run)}`;
  openPublishDialog({ item, body, runID: run.id });
}

function publishModes(item) {
  if (item.kind !== "pr") return [["comment", "评论"]];
  return [
    ["comment", "普通评论"],
    ["review:COMMENT", "评审 · 仅评论"],
    ["review:REQUEST_CHANGES", "评审 · 请求修改"],
    ["review:APPROVE", "评审 · 批准"],
  ];
}

function openPublishDialog({ item, body, runID = null, fromComposer = false, mode = "comment" }) {
  wb.publishContext = { item, runID, fromComposer, mode };
  const login = app.viewer?.login;
  dom.publishTarget.replaceChildren(
    h("span", { class: `badge ${item.kind === "pr" ? "badge-pr" : "badge-issue"}`, text: item.kind === "pr" ? "PR" : "Issue" }),
    h("a", { href: safeURL(item.url), target: "_blank", rel: "noopener noreferrer", text: `${item.repo}#${item.number}` }),
    h("span", { class: "confirm-target-title", text: item.title }),
    h("span", { class: "toolbar-spacer" }),
    login ? h("span", { class: "muted" }, "发布者 ", h("strong", { text: `@${login}` })) : h("span", { class: "warn-text", text: "未读取到 GitHub 账号" }),
  );
  const modes = publishModes(item);
  dom.publishModes.hidden = modes.length < 2;
  dom.publishModes.replaceChildren(
    ...modes.map(([value, label]) =>
      h(
        "button",
        {
          type: "button",
          class: value === mode ? "is-active" : "",
          dataset: { mode: value },
          onclick: () => {
            wb.publishContext.mode = value;
            for (const button of dom.publishModes.querySelectorAll("button")) {
              button.classList.toggle("is-active", button.dataset.mode === value);
            }
            updatePublishState();
          },
        },
        label,
      ),
    ),
  );
  dom.publishBody.value = body || "";
  setPublishTab("edit");
  updatePublishState();
  if (!dom.publishDialog.open) dom.publishDialog.showModal();
  dom.publishBody.focus();
  dom.publishBody.setSelectionRange(0, 0);
  dom.publishBody.scrollTop = 0;
}

function setPublishTab(tab) {
  for (const button of document.querySelectorAll("[data-publish-tab]")) {
    button.classList.toggle("is-active", button.dataset.publishTab === tab);
  }
  const preview = tab === "preview";
  dom.publishBody.hidden = preview;
  dom.publishPreview.hidden = !preview;
  if (preview) {
    dom.publishPreview.replaceChildren(...renderMarkdown(dom.publishBody.value || "（空）").childNodes);
  }
}

function updatePublishState() {
  const context = wb.publishContext;
  if (!context) return;
  const length = [...dom.publishBody.value].length;
  dom.publishLength.textContent = `${length} 字`;
  const approve = context.mode === "review:APPROVE";
  const empty = !dom.publishBody.value.trim();
  dom.publishSubmit.disabled = (empty && !approve) || length > 65000;
  const labels = {
    comment: "确认发布评论",
    "review:COMMENT": "确认提交评审",
    "review:REQUEST_CHANGES": "确认请求修改",
    "review:APPROVE": "确认批准",
  };
  dom.publishSubmit.textContent = labels[context.mode] || "确认发布";
  dom.publishSubmit.className = `button ${context.mode === "review:REQUEST_CHANGES" ? "button-danger" : "button-primary"}`;
}

async function submitPublish(event) {
  event.preventDefault();
  const context = wb.publishContext;
  if (!context) return;
  const [mode, reviewEvent] = context.mode.split(":");
  dom.publishSubmit.disabled = true;
  dom.publishSubmit.textContent = "正在发布…";
  try {
    const response = await api(`/api/items/${context.item.id}/publish`, {
      method: "POST",
      body: {
        mode,
        event: reviewEvent || "",
        body: dom.publishBody.value,
        run_id: context.runID || 0,
        confirm: true,
      },
    });
    dom.publishDialog.close();
    toast("已发布到 GitHub", "success");
    if (context.fromComposer && wb.drawer.itemID === context.item.id) wb.drawer.draft = "";
    if (wb.drawer.itemID === context.item.id) {
      loadDrawerDetail(true);
      if (context.runID) refreshAfterRunMutation({ id: context.runID });
    }
    if (response?.url) console.info("published:", response.url);
  } catch (error) {
    toast(error.message);
    updatePublishState();
  }
}

/* ---------- 推送确认 ---------- */

function canWritePermission(permission) {
  return ["ADMIN", "MAINTAIN", "WRITE"].includes(String(permission || "").toUpperCase());
}

async function openApplyDialog(run) {
  const item = itemByID(run.item_id);
  if (!item) {
    toast("找不到对应的条目");
    return;
  }
  wb.applyContext = { run, item, blocked: true };
  const isPR = run.kind === "pr";
  dom.applyTitle.textContent = isPR ? "确认提交并推送到 PR 分支" : "确认创建分支并发起 Pull Request";
  const branch = `gh-manager/issue-${run.number}-run${run.id}`;
  dom.applyTarget.replaceChildren(
    h("span", { class: `badge ${isPR ? "badge-pr" : "badge-issue"}`, text: isPR ? "PR" : "Issue" }),
    h("a", { href: safeURL(item.url), target: "_blank", rel: "noopener noreferrer", text: `${item.repo}#${item.number}` }),
    h("span", { class: "confirm-target-title", text: item.title }),
  );
  dom.applyHint.textContent = isPR
    ? `将以一个新提交推送到 ${run.head_repo || item.repo}:${run.head_ref}。`
    : `将从 ${run.base_ref} 创建分支 ${branch}，推送到 ${item.repo} 并创建 Pull Request。`;
  dom.applyPRFields.hidden = isPR;
  dom.applyCommit.value = isPR
    ? `fix: address review feedback (#${run.number})`
    : `fix: ${item.title} (#${run.number})`.slice(0, 200);
  if (!isPR) {
    dom.applyPRTitle.value = `Fix #${run.number}: ${item.title}`.slice(0, 250);
    dom.applyPRBody.value = `Closes #${run.number}\n\n${(run.result || "").trim()}`.slice(0, 60000);
    dom.applyDraft.checked = true;
  }
  dom.applyWarnings.replaceChildren(h("div", { class: "apply-warning tone-muted", text: isPR ? "正在检查 PR 的最新状态…" : "正在读取改动…" }));
  dom.applyDiff.replaceChildren(h("div", { class: "drawer-loading", text: "正在读取改动…" }));
  dom.applySubmit.disabled = true;
  dom.applySubmit.textContent = isPR ? "确认提交并推送" : "确认推送并创建 PR";
  if (!dom.applyDialog.open) dom.applyDialog.showModal();

  const [diffResult, detailResult] = await Promise.allSettled([
    api(`/api/runs/${run.id}/diff`),
    isPR ? api(`/api/items/${run.item_id}/detail?refresh=1`) : Promise.resolve(null),
  ]);
  if (wb.applyContext?.run !== run) return;
  const warnings = [];
  let blocked = false;
  if (diffResult.status === "fulfilled") {
    renderDiffResult(dom.applyDiff, diffResult.value);
    if (!diffResult.value?.patch && !diffResult.value?.stat) {
      warnings.push(["工作区中没有可提交的改动。", "danger"]);
      blocked = true;
    }
  } else {
    dom.applyDiff.replaceChildren(h("div", { class: "run-error", text: diffResult.reason.message }));
    warnings.push([`读取改动失败：${diffResult.reason.message}`, "danger"]);
    blocked = true;
  }
  if (isPR) {
    if (detailResult.status === "fulfilled" && detailResult.value) {
      const detail = detailResult.value;
      if (detail.state !== "open") {
        warnings.push(["PR 已关闭或已合并，不能推送。", "danger"]);
        blocked = true;
      }
      if (detail.head_oid && run.base_sha && detail.head_oid !== run.base_sha) {
        warnings.push(["Agent 开始运行后 PR 分支有了新提交。为避免覆盖他人的改动，请重新运行操作。", "danger"]);
        blocked = true;
      }
      if (detail.is_cross_repository) {
        if (!detail.maintainer_can_modify) {
          warnings.push(["这是来自 fork 的 PR，且作者没有允许维护者修改，无法推送。", "danger"]);
          blocked = true;
        } else {
          warnings.push([`这是来自 fork 的 PR，改动会推送到作者的仓库 ${detail.head_repo}（作者已允许维护者修改）。`, "warn"]);
        }
      } else if (!canWritePermission(detail.viewer_permission)) {
        warnings.push([`你对 ${item.repo} 的权限是 ${detail.viewer_permission || "未知"}，可能无法推送。`, "warn"]);
      }
    } else if (detailResult.status === "rejected") {
      warnings.push([`无法确认 PR 最新状态：${detailResult.reason.message}。推送时服务端仍会再次检查。`, "warn"]);
    }
  }
  if (!warnings.length) {
    warnings.push([isPR ? "PR 状态正常：分支未被他人更新，可以推送。" : "将创建新分支，不会改动默认分支。", "success"]);
  }
  dom.applyWarnings.replaceChildren(
    ...warnings.map(([text, tone]) => h("div", { class: `apply-warning tone-${tone}`, text })),
  );
  wb.applyContext.blocked = blocked;
  dom.applySubmit.disabled = blocked;
}

async function submitApply(event) {
  event.preventDefault();
  const context = wb.applyContext;
  if (!context || context.blocked) return;
  const { run } = context;
  if (!dom.applyCommit.value.trim()) {
    toast("请填写提交说明");
    return;
  }
  dom.applySubmit.disabled = true;
  dom.applySubmit.textContent = "正在提交并推送…";
  try {
    const result = await api(`/api/runs/${run.id}/apply`, {
      method: "POST",
      body: {
        commit_message: dom.applyCommit.value,
        pr_title: dom.applyPRTitle.value,
        pr_body: dom.applyPRBody.value,
        draft: dom.applyDraft.checked,
        confirm: true,
      },
    });
    dom.applyDialog.close();
    toast(result.warning ? `已推送，但 ${result.warning}` : "改动已推送", result.warning ? "error" : "success");
    await refreshAfterRunMutation(run);
    if (wb.drawer.itemID === run.item_id) loadDrawerDetail(true);
  } catch (error) {
    toast(error.message);
    dom.applySubmit.disabled = false;
    dom.applySubmit.textContent = run.kind === "pr" ? "确认提交并推送" : "确认推送并创建 PR";
  }
}

async function openDiffDialog(run) {
  dom.diffTitle.textContent = `任务 #${run.id} 的改动`;
  dom.diffBody.replaceChildren(h("div", { class: "drawer-loading", text: "正在读取改动…" }));
  if (!dom.diffDialog.open) dom.diffDialog.showModal();
  try {
    renderDiffResult(dom.diffBody, await api(`/api/runs/${run.id}/diff`));
  } catch (error) {
    dom.diffBody.replaceChildren(h("div", { class: "run-error", text: error.message }));
  }
}

/* ---------- 最近任务弹层 ---------- */

async function openRunsPopover() {
  const content = h(
    "div",
    { class: "runs-popover" },
    h("div", { class: "popover-title" }, "最近的 AI 任务"),
    h("div", { class: "drawer-loading", text: "正在读取…" }),
  );
  openPopover(dom.agentIndicator, content, { width: 420 });
  let runs = [];
  try {
    runs = await api("/api/runs?limit=30");
  } catch (error) {
    content.lastChild.textContent = error.message;
    return;
  }
  if (dom.popover.hidden || !dom.popover.contains(content)) return;
  const list = h("div", { class: "runs-popover-list" });
  if (!runs.length) list.append(h("div", { class: "popover-empty", text: "还没有任务。勾选表格中的条目后点击“运行 AI 操作”。" }));
  for (const run of runs) {
    list.append(
      h(
        "button",
        {
          type: "button",
          class: "runs-popover-row",
          onclick: () => {
            closePopover();
            openItemDrawer(run.item_id, { tab: "ai", runID: run.id });
          },
        },
        runStatusChip(run),
        h(
          "span",
          { class: "runs-popover-copy" },
          h("strong", { text: `${run.repo}#${run.number}` }),
          h("small", { text: `${run.action_name} · ${formatRelative(run.created_at)}` }),
        ),
      ),
    );
  }
  content.lastChild.replaceWith(list);
  const status = cliStatus("claude");
  const codex = cliStatus("codex");
  content.append(
    h(
      "div",
      { class: "popover-footer" },
      h("span", {
        text: [status, codex]
          .filter(Boolean)
          .map((cli) => `${agentLabel(cli.name)} ${cli.available ? cli.version || "可用" : "不可用"}`)
          .join(" · "),
      }),
      h("button", { type: "button", class: "link-button", onclick: () => { closePopover(); openSettingsDialog(); } }, "设置"),
    ),
  );
}

/* ---------- AI 操作管理 ---------- */

const DEFAULT_ACTION_PROMPT = `请处理 {{kind}} {{repo}}#{{number}}：{{title}}

（在这里描述希望 Agent 完成的事情，以及最终回复的格式。）`;

async function reloadActions() {
  try {
    app.actions = await api("/api/actions");
  } catch (error) {
    toast(error.message);
  }
  renderActionsList();
}

function openActionsDialog() {
  renderActionsList();
  if (wb.editingActionID == null) fillActionForm(app.actions[0] || null);
  if (!dom.actionsDialog.open) dom.actionsDialog.showModal();
  loadSkills("")
    .then((entry) => fillSkillList(dom.actionSkillList, entry.skills, dom.actionAgent.value))
    .catch(() => {});
}

function renderActionsList() {
  dom.actionsList.replaceChildren(
    ...app.actions.map((action) =>
      h(
        "button",
        {
          type: "button",
          class: `action-row${wb.editingActionID === action.id ? " is-active" : ""}`,
          onclick: () => fillActionForm(action),
        },
        h("strong", { text: action.name }),
        h(
          "small",
          { class: "muted" },
          `${agentLabel(action.agent)} · ${modeLabel(action.mode)} · ${appliesLabel(action.applies_to)}${action.skill ? ` · ${action.skill}` : ""}`,
        ),
      ),
    ),
    h("button", { type: "button", class: "action-row is-new", onclick: () => fillActionForm(null) }, "＋ 新建操作"),
  );
}

function fillActionForm(action) {
  wb.editingActionID = action ? action.id : null;
  dom.actionFormTitle.textContent = action ? `编辑：${action.name}` : "新建操作";
  dom.actionName.value = action?.name || "";
  dom.actionAgent.value = action?.agent || "claude";
  dom.actionMode.value = action?.mode || "read";
  dom.actionAppliesTo.value = action?.applies_to || "all";
  dom.actionSkill.value = action?.skill || "";
  dom.actionModel.value = action?.model || "";
  dom.actionPrompt.value = action ? action.prompt : DEFAULT_ACTION_PROMPT;
  dom.actionTools.value = action?.allowed_tools || "";
  dom.actionDelete.hidden = !action;
  renderActionsList();
}

async function saveAction(event) {
  event.preventDefault();
  const payload = {
    name: dom.actionName.value.trim(),
    agent: dom.actionAgent.value,
    mode: dom.actionMode.value,
    applies_to: dom.actionAppliesTo.value,
    skill: dom.actionSkill.value.trim(),
    model: dom.actionModel.value.trim(),
    prompt: dom.actionPrompt.value,
    allowed_tools: dom.actionTools.value.trim(),
  };
  if (!payload.name) {
    toast("请填写操作名称");
    return;
  }
  if (!payload.prompt.trim() && !payload.skill) {
    toast("提示词和 SKILL 至少填写一个");
    return;
  }
  dom.actionSave.disabled = true;
  try {
    const saved =
      wb.editingActionID != null
        ? await api(`/api/actions/${wb.editingActionID}`, { method: "PATCH", body: payload })
        : await api("/api/actions", { method: "POST", body: payload });
    wb.editingActionID = saved.id;
    await reloadActions();
    fillActionForm(app.actions.find((action) => action.id === saved.id) || saved);
    toast("操作已保存", "success");
  } catch (error) {
    toast(error.message);
  } finally {
    dom.actionSave.disabled = false;
  }
}

async function deleteAction() {
  const action = app.actions.find((candidate) => candidate.id === wb.editingActionID);
  if (!action) return;
  const ok = await confirmAction({
    title: "删除 AI 操作？",
    message: `将删除“${action.name}”。已有的任务记录会保留。`,
    confirmLabel: "删除",
    danger: true,
  });
  if (!ok) return;
  try {
    await api(`/api/actions/${action.id}`, { method: "DELETE" });
    wb.editingActionID = null;
    await reloadActions();
    fillActionForm(app.actions[0] || null);
    toast("操作已删除", "success");
  } catch (error) {
    toast(error.message);
  }
}

/* ---------- 设置 ---------- */

function renderEnvironmentGrid() {
  const env = wb.env || {};
  const rows = [];
  rows.push(
    h(
      "div",
      { class: "env-row" },
      h("span", { class: "meta-label", text: "GitHub 账号" }),
      env.viewer
        ? h("span", {}, person(env.viewer.login), env.viewer.name ? h("span", { class: "muted", text: ` ${env.viewer.name}` }) : null)
        : h("span", { class: "warn-text", text: env.viewer_error || "检测中…" }),
    ),
  );
  for (const name of ["claude", "codex"]) {
    const status = cliStatus(name);
    rows.push(
      h(
        "div",
        { class: "env-row" },
        h("span", { class: "meta-label", text: name === "claude" ? "Claude Code" : "Codex" }),
        status
          ? status.available
            ? h("span", {}, h("span", { class: "status-chip tone-success", text: status.version || "可用" }), h("code", { class: "muted", text: ` ${status.path}` }))
            : h("span", { class: "warn-text", text: status.error || `未找到命令 ${status.command}` })
          : h("span", { class: "muted", text: "检测中…" }),
      ),
    );
  }
  if (env.workspace_dir) {
    rows.push(h("div", { class: "env-row" }, h("span", { class: "meta-label", text: "代码工作区" }), h("code", { text: env.workspace_dir })));
  }
  if (env.runs_dir) {
    rows.push(h("div", { class: "env-row" }, h("span", { class: "meta-label", text: "任务日志" }), h("code", { text: env.runs_dir })));
  }
  rows.push(
    h(
      "div",
      { class: "env-row" },
      h("span", {}),
      h("button", { type: "button", class: "link-button", onclick: () => loadEnvironment(true) }, "重新检测"),
    ),
  );
  dom.envGrid.replaceChildren(...rows);
}

async function openSettingsDialog() {
  renderEnvironmentGrid();
  if (!dom.settingsDialog.open) dom.settingsDialog.showModal();
  try {
    const settings = await api("/api/settings");
    const agent = settings.agent || {};
    const sync = settings.sync || {};
    dom.setClaudeCommand.value = agent.claude_command || "";
    dom.setCodexCommand.value = agent.codex_command || "";
    dom.setClaudeModel.value = agent.claude_model || "";
    dom.setCodexModel.value = agent.codex_model || "";
    dom.setMaxConcurrent.value = agent.max_concurrent || "";
    dom.setTimeout.value = agent.timeout_minutes || "";
    dom.setWorkspaceDir.value = agent.workspace_dir || "";
    dom.setWorkspaceDir.placeholder = settings.effective_workspace_dir || "";
    dom.setStaleDays.value = sync.stale_days || "";
    dom.setReviewStaleDays.value = sync.review_stale_days || "";
    dom.setClosedWindowDays.value = sync.closed_window_days || "";
    dom.setPageSize.value = sync.request_page_size || "";
    dom.settingsPath.textContent = settings.config_path ? `保存到 ${settings.config_path}` : "";
    wb.loadedSettings = settings;
  } catch (error) {
    toast(error.message);
  }
  loadEnvironment(true);
}

async function saveSettings(event) {
  event.preventDefault();
  const number = (input) => (input.value === "" ? 0 : Number(input.value));
  const previous = wb.loadedSettings || {};
  const payload = {
    agent: {
      claude_command: dom.setClaudeCommand.value.trim(),
      codex_command: dom.setCodexCommand.value.trim(),
      claude_model: dom.setClaudeModel.value.trim(),
      codex_model: dom.setCodexModel.value.trim(),
      max_concurrent: number(dom.setMaxConcurrent),
      timeout_minutes: number(dom.setTimeout),
      workspace_dir: dom.setWorkspaceDir.value.trim(),
    },
    sync: {
      stale_days: number(dom.setStaleDays),
      review_stale_days: number(dom.setReviewStaleDays),
      closed_window_days: number(dom.setClosedWindowDays),
      request_page_size: number(dom.setPageSize),
    },
  };
  dom.settingsSave.disabled = true;
  try {
    await api("/api/settings", { method: "PUT", body: payload });
    dom.settingsDialog.close();
    const workspaceChanged = (previous.agent?.workspace_dir || "") !== payload.agent.workspace_dir;
    toast(workspaceChanged ? "设置已保存；工作区目录在重启 make run 后生效" : "设置已保存", "success");
    loadEnvironment(true);
    loadState({ silent: true });
  } catch (error) {
    toast(error.message);
  } finally {
    dom.settingsSave.disabled = false;
  }
}

/* ---------- 从组织导入仓库 ---------- */

async function loadOrgRepos(event) {
  event.preventDefault();
  const owner = dom.orgImportOwner.value.trim();
  if (!owner) return;
  const submit = dom.orgImportForm.querySelector('button[type="submit"]');
  submit.disabled = true;
  dom.orgImportList.replaceChildren(h("div", { class: "drawer-loading", text: "正在读取仓库列表…" }));
  try {
    const response = await api(`/api/github/repos?owner=${encodeURIComponent(owner)}`);
    wb.orgRepos = response.repos || [];
    wb.orgManaged = new Set((response.managed || []).map((name) => name.toLowerCase()));
    wb.orgSelected = new Set();
    renderOrgRepos();
  } catch (error) {
    dom.orgImportList.replaceChildren(h("div", { class: "run-error", text: error.message }));
  } finally {
    submit.disabled = false;
  }
}

function renderOrgRepos() {
  const available = wb.orgRepos.filter((name) => !wb.orgManaged.has(name.toLowerCase()));
  if (!wb.orgRepos.length) {
    dom.orgImportList.replaceChildren(h("div", { class: "popover-empty", text: "没有找到仓库。" }));
    return;
  }
  const selectAll = h("input", { type: "checkbox" });
  selectAll.checked = available.length > 0 && available.every((name) => wb.orgSelected.has(name));
  selectAll.addEventListener("change", () => {
    for (const name of available) {
      if (selectAll.checked) wb.orgSelected.add(name);
      else wb.orgSelected.delete(name);
    }
    renderOrgRepos();
  });
  const add = h(
    "button",
    {
      type: "button",
      class: "button button-primary button-small",
      disabled: wb.orgSelected.size === 0,
      onclick: async () => {
        add.disabled = true;
        try {
          const response = await api("/api/repos/batch", { method: "POST", body: { repos: [...wb.orgSelected] } });
          toast(`已添加 ${response.added} 个仓库，同步后即可看到其中的 PR 与 Issue`, "success");
          for (const name of wb.orgSelected) wb.orgManaged.add(name.toLowerCase());
          wb.orgSelected.clear();
          renderOrgRepos();
          await loadState({ silent: true });
        } catch (error) {
          toast(error.message);
          add.disabled = false;
        }
      },
    },
    `添加选中的 ${wb.orgSelected.size} 个仓库`,
  );
  dom.orgImportList.replaceChildren(
    h(
      "div",
      { class: "org-import-head" },
      h("label", { class: "picker-row" }, selectAll, h("span", { text: `全选未追踪的仓库（${available.length}）` })),
      h("span", { class: "toolbar-spacer" }),
      add,
    ),
    h(
      "div",
      { class: "org-import-rows" },
      wb.orgRepos.map((name) => {
        const managed = wb.orgManaged.has(name.toLowerCase());
        const checkbox = h("input", { type: "checkbox", checked: managed || wb.orgSelected.has(name), disabled: managed });
        checkbox.addEventListener("change", () => {
          if (checkbox.checked) wb.orgSelected.add(name);
          else wb.orgSelected.delete(name);
          renderOrgRepos();
        });
        return h(
          "label",
          { class: `picker-row${managed ? " is-disabled" : ""}` },
          checkbox,
          h("span", { class: "picker-name", text: name }),
          managed ? h("small", { class: "muted", text: "已追踪" }) : null,
        );
      }),
    ),
  );
}

/* ---------- 初始化 ---------- */

function cacheWorkbenchDom() {
  const ids = [
    "agentIndicator", "claudeDot", "codexDot", "agentActive", "openSettings", "viewTabs", "viewerLabel",
    "manageActions", "batchBar", "batchCount", "batchRun", "batchClear", "drawer", "drawerHeader",
    "drawerTabs", "drawerBody", "orgImportForm", "orgImportOwner", "orgImportList", "runDialog", "runForm",
    "runTargets", "runAction", "runSkill", "runSkillList", "runActionHint", "runSkillHint", "runExtra",
    "runSubmit", "publishDialog", "publishForm", "publishTarget", "publishModes", "publishLength",
    "publishBody", "publishPreview", "publishSubmit", "applyDialog", "applyForm", "applyTitle",
    "applyTarget", "applyWarnings", "applyCommit", "applyPRFields", "applyPRTitle", "applyPRBody",
    "applyDraft", "applyDiff", "applyHint", "applySubmit", "diffDialog", "diffTitle", "diffBody",
    "actionsDialog", "actionsList", "actionForm", "actionFormTitle", "actionName", "actionAgent",
    "actionMode", "actionAppliesTo", "actionSkill", "actionSkillList", "actionModel", "actionPrompt",
    "actionTools", "actionDelete", "actionReset", "actionSave", "settingsDialog", "settingsForm", "envGrid",
    "setClaudeCommand", "setCodexCommand", "setClaudeModel", "setCodexModel", "setMaxConcurrent",
    "setTimeout", "setWorkspaceDir", "setStaleDays", "setReviewStaleDays", "setClosedWindowDays",
    "setPageSize", "settingsPath", "settingsSave", "confirmDialog", "confirmTitle", "confirmMessage",
    "confirmCancel", "confirmOK",
  ];
  for (const id of ids) {
    dom[id] = document.getElementById(id);
    if (!dom[id]) console.warn(`missing element #${id}`);
  }
}

const DRAWER_WIDTH_KEY = "gh-manager:drawer-width";

function setDrawerWidth(width) {
  document.documentElement.style.setProperty("--drawer-width", `min(${Math.round(width)}px, 100vw)`);
}

function initDrawerResize() {
  const saved = Number(localStorage.getItem(DRAWER_WIDTH_KEY));
  if (saved >= 420) setDrawerWidth(saved);
  const handle = dom.drawer.querySelector(".drawer-resize");
  if (!handle) return;
  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    event.preventDefault();
    handle.setPointerCapture(event.pointerId);
    document.body.classList.add("is-resizing-drawer");
    const move = (moveEvent) => {
      const max = Math.max(420, window.innerWidth - 160);
      setDrawerWidth(Math.min(Math.max(window.innerWidth - moveEvent.clientX, 420), max));
    };
    const stop = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", stop);
      handle.removeEventListener("pointercancel", stop);
      document.body.classList.remove("is-resizing-drawer");
      localStorage.setItem(DRAWER_WIDTH_KEY, String(Math.round(dom.drawer.getBoundingClientRect().width)));
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", stop);
    handle.addEventListener("pointercancel", stop);
  });
  handle.addEventListener("dblclick", () => {
    localStorage.removeItem(DRAWER_WIDTH_KEY);
    document.documentElement.style.removeProperty("--drawer-width");
  });
}

function anyDialogOpen() {
  return Boolean(document.querySelector("dialog[open]"));
}

function bindWorkbenchEvents() {
  dom.viewTabs.addEventListener("click", (event) => {
    const tab = event.target.closest("[data-view]");
    if (!tab || tab.disabled) return;
    app.filters.view = tab.dataset.view;
    renderStats();
    renderTable();
  });
  dom.agentIndicator.addEventListener("click", openRunsPopover);
  dom.openSettings.addEventListener("click", openSettingsDialog);
  dom.manageActions.addEventListener("click", openActionsDialog);
  dom.batchRun.addEventListener("click", () => openRunDialog([...app.selected]));
  dom.batchClear.addEventListener("click", () => {
    app.selected.clear();
    renderTable();
  });

  dom.runForm.addEventListener("submit", submitRunDialog);
  dom.runAction.addEventListener("change", syncRunActionDefaults);
  dom.publishForm.addEventListener("submit", submitPublish);
  dom.publishBody.addEventListener("input", updatePublishState);
  for (const button of document.querySelectorAll("[data-publish-tab]")) {
    button.addEventListener("click", () => setPublishTab(button.dataset.publishTab));
  }
  dom.applyForm.addEventListener("submit", submitApply);
  dom.actionForm.addEventListener("submit", saveAction);
  dom.actionReset.addEventListener("click", () => fillActionForm(null));
  dom.actionDelete.addEventListener("click", deleteAction);
  dom.actionAgent.addEventListener("change", () => {
    const cached = wb.skills.get("");
    if (cached) fillSkillList(dom.actionSkillList, cached.skills, dom.actionAgent.value);
  });
  dom.settingsForm.addEventListener("submit", saveSettings);
  dom.orgImportForm.addEventListener("submit", loadOrgRepos);
  dom.confirmCancel.addEventListener("click", () => dom.confirmDialog.close("cancel"));
  dom.diffDialog.addEventListener("click", (event) => {
    if (event.target === dom.diffDialog) dom.diffDialog.close();
  });

  // 捕获阶段执行：先于 app.js 关闭弹层的处理，避免一次 Esc 同时关掉弹层和抽屉。
  document.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || dom.drawer.hidden || anyDialogOpen() || !dom.popover.hidden) return;
      const target = event.target;
      if (target instanceof HTMLElement && (target.tagName === "TEXTAREA" || target.tagName === "INPUT")) {
        target.blur();
        return;
      }
      closeDrawer();
    },
    true,
  );
  window.addEventListener("beforeunload", () => {
    closeRunEvents();
    wb.runStream?.close();
  });
}

function initWorkbench() {
  cacheWorkbenchDom();
  bindWorkbenchEvents();
  initDrawerResize();
  renderAgentIndicator();
  loadEnvironment();
  connectRunStream();
}
