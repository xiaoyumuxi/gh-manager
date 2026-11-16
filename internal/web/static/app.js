const app = {
  data: null,
  items: [],
  tags: [],
  fields: [],
  repositories: [],
  filters: { query: "", state: "", kind: "", tag: "", repos: [], stale: false },
  sort: { key: "updated_at", direction: "desc" },
  pollTimer: null,
  loadError: "",
};

const dom = {};
let toastTimer = null;
let popoverCleanup = null;

function h(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value == null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "html") node.innerHTML = value;
    else if (key === "dataset") Object.assign(node.dataset, value);
    else if (key === "style") {
      for (const [styleKey, styleValue] of Object.entries(value)) {
        if (styleKey.startsWith("--")) node.style.setProperty(styleKey, styleValue);
        else node.style[styleKey] = styleValue;
      }
    } else if (key.startsWith("on") && typeof value === "function") {
      node.addEventListener(key.slice(2).toLowerCase(), value);
    } else if (key in node) {
      try {
        node[key] = value;
      } catch {
        node.setAttribute(key, String(value));
      }
    } else {
      node.setAttribute(key, String(value));
    }
  }
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function icon(path, viewBox = "0 0 20 20") {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", viewBox);
  svg.setAttribute("aria-hidden", "true");
  svg.innerHTML = path;
  return svg;
}

async function api(path, options = {}) {
  const method = options.method || "GET";
  const headers = { ...(options.headers || {}) };
  let body = options.body;
  if (body != null && typeof body !== "string") {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(body);
  }
  const response = await fetch(path, {
    ...options,
    method,
    headers,
    body,
    credentials: "same-origin",
  });
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) {
    throw new Error(payload?.error || `请求失败（${response.status}）`);
  }
  return payload;
}

function toast(message, type = "error") {
  clearTimeout(toastTimer);
  dom.toast.hidden = false;
  dom.toast.className = `toast${type === "success" ? " is-success" : ""}`;
  const symbol =
    type === "success"
      ? '<path d="m5 10.2 3.1 3.1 6.9-7.1" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>'
      : '<circle cx="10" cy="10" r="7.2" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M10 6.2v4.6M10 13.6v.2" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>';
  dom.toast.replaceChildren(icon(symbol), h("span", { text: message }));
  toastTimer = setTimeout(() => {
    dom.toast.hidden = true;
  }, 4400);
}

function parseDate(value) {
  if (!value) return null;
  const normalized = String(value).replace(/(\.\d{3})\d+/, "$1");
  const date = new Date(normalized);
  return Number.isNaN(date.getTime()) ? null : date;
}

function pad(value) {
  return String(value).padStart(2, "0");
}

function formatDateTime(value) {
  const date = parseDate(value);
  if (!date) return "—";
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}`;
}

function formatShortDate(value) {
  const date = parseDate(value);
  if (!date) return "—";
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(
    date.getMinutes(),
  )}`;
}

function formatRelative(value) {
  const date = parseDate(value);
  if (!date) return "—";
  const seconds = Math.round((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days} 天前`;
  const now = new Date();
  if (date.getFullYear() === now.getFullYear()) {
    return `${date.getMonth() + 1}月${date.getDate()}日`;
  }
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())}`;
}

function normalizeColor(value) {
  const color = String(value || "");
  return /^#[0-9a-f]{6}$/i.test(color) ? color : "#8C8C8C";
}

function colorWithAlpha(color, alpha) {
  return `${normalizeColor(color)}${alpha}`;
}

function shadeColor(color, factor) {
  const hex = normalizeColor(color).slice(1);
  const channels = [0, 2, 4].map((offset) => parseInt(hex.slice(offset, offset + 2), 16));
  return `#${channels
    .map((channel) =>
      Math.max(0, Math.min(255, Math.round(channel * (1 + factor))))
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

function tagChip(tag) {
  const color = normalizeColor(tag.color);
  return h(
    "span",
    {
      class: "tag-chip",
      title: tag.name,
      style: {
        color: shadeColor(color, -0.16),
        background: colorWithAlpha(color, "16"),
        borderColor: colorWithAlpha(color, "35"),
      },
    },
    h("span", { text: tag.name }),
  );
}

function labelChip(label) {
  return h(
    "span",
    { class: "label-chip", title: label.name },
    h("span", { class: "label-dot", style: { background: normalizeColor(label.color) } }),
    h("span", { text: label.name }),
  );
}

function avatarImage(login, size = 21) {
  const image = h("img", {
    class: "avatar",
    src: `https://github.com/${encodeURIComponent(login)}.png?size=${size * 2}`,
    alt: "",
    loading: "lazy",
    width: size,
    height: size,
  });
  image.addEventListener("error", () => {
    image.replaceWith(
      h("span", {
        class: "avatar",
        text: String(login).slice(0, 1).toUpperCase(),
        style: { width: `${size}px`, height: `${size}px` },
      }),
    );
  });
  return image;
}

function person(login) {
  if (!login) return h("span", { class: "muted", text: "—" });
  return h(
    "span",
    { class: "person", title: login },
    avatarImage(login),
    h("span", { class: "person-name", text: login }),
  );
}

function assignee(login) {
  const image = avatarImage(login, 16);
  image.classList.remove("avatar");
  return h(
    "span",
    { class: "assignee", title: login },
    image,
    h("span", { text: `@${login}` }),
  );
}

function fallback(value) {
  return h("span", { class: "muted", text: value });
}

function safeURL(value) {
  try {
    const parsed = new URL(value);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") return parsed.href;
  } catch {
    return "#";
  }
  return "#";
}

function currentTags(item) {
  return Array.isArray(item.tags) ? item.tags : [];
}

function currentLabels(item) {
  return Array.isArray(item.github_labels) ? item.github_labels : [];
}

function currentAssignees(item) {
  return Array.isArray(item.assignees) ? item.assignees : [];
}

function fieldValue(item, field) {
  return item.fields ? item.fields[String(field.id)] : undefined;
}

function sameRepo(left, right) {
  return String(left).toLocaleLowerCase() === String(right).toLocaleLowerCase();
}

function repoStatus(fullName) {
  return (app.data?.repo_statuses || []).find((repo) =>
    sameRepo(repo.full_name, fullName),
  );
}

function repoItemCount(fullName) {
  return app.items.filter((item) => sameRepo(item.repo, fullName)).length;
}

function syncIsRunning() {
  return Boolean(app.data?.sync_status?.running ?? app.data?.syncing);
}

function hasFilters() {
  return Boolean(
    app.filters.query ||
      app.filters.state ||
      app.filters.kind ||
      app.filters.tag ||
      app.filters.repos.length ||
      app.filters.stale,
  );
}

function applyFilters() {
  const query = app.filters.query.trim().toLocaleLowerCase("zh-CN");
  return app.items.filter((item) => {
    if (app.filters.state) {
      const closed = app.filters.state === "closed";
      const matchesState =
        item.state === app.filters.state || (closed && item.state === "merged");
      if (!matchesState) return false;
    }
    if (app.filters.kind && item.kind !== app.filters.kind) return false;
    if (app.filters.tag && !currentTags(item).some((tag) => String(tag.id) === app.filters.tag)) {
      return false;
    }
    if (
      app.filters.repos.length &&
      !app.filters.repos.some((repo) => sameRepo(repo, item.repo))
    ) {
      return false;
    }
    if (app.filters.stale && !item.stale && !item.review_stale) return false;
    if (!query) return true;
    const haystack = [
      item.title,
      item.repo,
      `#${item.number}`,
      String(item.number),
      item.author,
      item.milestone,
      ...currentTags(item).map((tag) => tag.name),
      ...currentLabels(item).map((label) => label.name),
      ...currentAssignees(item),
      ...(Array.isArray(item.reviewers) ? item.reviewers : []),
    ]
      .filter(Boolean)
      .join(" ")
      .toLocaleLowerCase("zh-CN");
    if (haystack.includes(query)) return true;
    return app.fields.some((field) => {
      const value = fieldValue(item, field);
      if (value == null) return false;
      return [].concat(value).join(" ").toLocaleLowerCase("zh-CN").includes(query);
    });
  });
}

function isEmptyValue(value) {
  return value == null || value === "" || (Array.isArray(value) && value.length === 0);
}

function compareValues(left, right) {
  if (typeof left === "number" && typeof right === "number") return left - right;
  if (typeof left === "boolean" && typeof right === "boolean") {
    return Number(left) - Number(right);
  }
  return String(left).localeCompare(String(right), "zh-CN", {
    numeric: true,
    sensitivity: "base",
  });
}

function sortValue(item, key) {
  if (key === "title") return item.title?.toLocaleLowerCase("zh-CN");
  if (key === "kind") return item.kind;
  if (key === "state") return item.state;
  if (key === "author") return item.author;
  if (key === "updated_at") {
    const date = parseDate(item.updated_at || item.created_at);
    return date ? date.getTime() : null;
  }
  if (key === "stale_days") return item.stale_days ?? 0;
  if (key.startsWith("field:")) {
    const field = app.fields.find((candidate) => `field:${candidate.id}` === key);
    if (!field) return null;
    const value = fieldValue(item, field);
    if (isEmptyValue(value)) return null;
    if (field.type === "number") return Number(value);
    if (field.type === "checkbox") return value === true ? 1 : 0;
    if (field.type === "date") {
      const date = parseDate(`${value}T00:00:00`);
      return date ? date.getTime() : null;
    }
    if (Array.isArray(value)) return value.join("、");
    return value;
  }
  return null;
}

function sortedItems(items) {
  const direction = app.sort.direction === "asc" ? 1 : -1;
  return [...items].sort((left, right) => {
    const leftValue = sortValue(left, app.sort.key);
    const rightValue = sortValue(right, app.sort.key);
    const leftEmpty = isEmptyValue(leftValue);
    const rightEmpty = isEmptyValue(rightValue);
    if (leftEmpty && rightEmpty) return 0;
    if (leftEmpty) return 1;
    if (rightEmpty) return -1;
    return compareValues(leftValue, rightValue) * direction;
  });
}

function columns() {
  return [
    { key: "title", label: "事项", class: "col-title", sortable: true },
    { key: "kind", label: "类型", class: "col-kind", sortable: true },
    { key: "state", label: "状态", class: "col-state", sortable: true },
    { key: "tags", label: "本地标签", class: "col-tags", sortable: false },
    { key: "author", label: "作者", class: "col-author", sortable: true },
    { key: "labels", label: "GitHub 标签", class: "col-labels", sortable: false },
    { key: "assignees", label: "指派", class: "col-assignees", sortable: false },
    { key: "updated_at", label: "更新", class: "col-updated", sortable: true },
    { key: "stale_days", label: "停滞", class: "col-stale", sortable: true },
    ...app.fields.map((field) => ({
      key: `field:${field.id}`,
      label: field.name,
      class: "col-field",
      sortable: true,
      field,
    })),
  ];
}

function stateLabel(state) {
  if (state === "merged") return "已合并";
  if (state === "closed") return "已关闭";
  return "打开中";
}

function reviewLabel(value) {
  const map = {
    APPROVED: ["评审通过", "success"],
    REVIEW_REQUIRED: ["待评审", "warn"],
    CHANGES_REQUESTED: ["需修改", "danger"],
  };
  return map[value] || null;
}

function ciLabel(value) {
  const map = {
    SUCCESS: ["检查通过", "success"],
    FAILURE: ["检查失败", "danger"],
    ERROR: ["检查异常", "danger"],
    PENDING: ["检查中", "warn"],
    EXPECTED: ["等待检查", "warn"],
  };
  return map[value] || null;
}

function tagCellContent(item) {
  const tags = currentTags(item);
  const shown = tags.slice(0, 2);
  const rest = tags.slice(2);
  const wrap = h("div", { class: "tag-cell" });
  shown.forEach((tag) => wrap.append(tagChip(tag)));
  if (rest.length) {
    wrap.append(
      h("span", {
        class: "tag-more",
        text: `+${rest.length}`,
        title: rest.map((tag) => tag.name).join("、"),
      }),
    );
  }
  const add = h(
    "button",
    {
      class: "tag-add",
      type: "button",
      title: "编辑本地标签",
      dataset: { action: "edit-tags", itemId: item.id },
    },
    tags.length ? "+" : "+ 标签",
  );
  wrap.append(add);
  return wrap;
}

function stateCell(item) {
  const wrap = h("div");
  const badges = h("div", { class: "chip-row" });
  badges.append(
    h("span", {
      class: `badge badge-${item.state === "merged" ? "merged" : item.state === "closed" ? "closed" : "open"}`,
      text: stateLabel(item.state),
    }),
  );
  if (item.is_draft) {
    badges.append(h("span", { class: "badge badge-draft", text: "草稿" }));
  }
  wrap.append(badges);
  const details = [];
  if (item.kind === "pr") {
    const review = reviewLabel(item.review_decision);
    if (review) details.push(review);
    const ci = ciLabel(item.ci_status);
    if (ci) details.push(ci);
    if (item.mergeable === "CONFLICTING") details.push(["有冲突", "danger"]);
  } else if (item.milestone) {
    details.push([`里程碑 ${item.milestone}`, ""]);
  }
  if (details.length) {
    const sub = h("div", { class: "cell-sub" });
    details.slice(0, 2).forEach(([label, tone], index) => {
      if (index) sub.append("·");
      sub.append(h("span", { class: tone, text: label }));
    });
    wrap.append(sub);
  }
  return wrap;
}

function updatedCell(item) {
  const value = item.updated_at || item.created_at;
  if (!value) return fallback("—");
  return h(
    "span",
    { class: "time-cell", title: `更新于 ${formatDateTime(value)}` },
    formatRelative(value),
    h("small", { text: formatShortDate(value) }),
  );
}

function staleCell(item) {
  if (item.state !== "open") return fallback("—");
  const days = Number(item.stale_days) || 0;
  if (item.review_stale) {
    return h("span", {
      class: "stale-badge is-review",
      text: `评审 ${days} 天`,
      title: item.stale ? `同时已停滞 ${days} 天` : `等待评审 ${days} 天`,
    });
  }
  if (item.stale) {
    return h("span", { class: "stale-badge is-stale", text: `停滞 ${days} 天` });
  }
  return h("span", { class: "stale-plain", text: `${days} 天` });
}

function fieldCell(item, field) {
  const value = fieldValue(item, field);
  const button = h("button", {
    class: "cell-edit",
    type: "button",
    title: `${field.name}：点击编辑`,
    dataset: { action: "edit-field", itemId: item.id, fieldId: field.id },
  });
  if (field.type === "checkbox") {
    button.append(
      h("span", {
        class: `checkmark${value === true ? " is-on" : ""}`,
        text: value === true ? "✓" : "",
      }),
    );
    return button;
  }
  if (isEmptyValue(value)) {
    button.append(h("span", { class: "cell-placeholder", text: "填写" }));
    return button;
  }
  if (Array.isArray(value)) {
    value.slice(0, 2).forEach((option) => {
      button.append(h("span", { class: "option-chip", text: option, title: option }));
    });
    if (value.length > 2) {
      button.append(h("span", { class: "tag-more", text: `+${value.length - 2}` }));
    }
    return button;
  }
  const display =
    field.type === "checkbox" ? (value ? "已勾选" : "未勾选") : String(value);
  button.append(h("span", { class: "cell-value", text: display, title: display }));
  return button;
}

function renderTableHead() {
  const headRow = h("tr");
  for (const column of columns()) {
    const th = h("th", { class: column.class });
    const inner = h(
      "div",
      { class: "th-inner" },
      h("span", { text: column.label, title: column.label }),
    );
    if (column.sortable) {
      th.dataset.sort = column.key;
      const active = app.sort.key === column.key;
      inner.append(
        h("span", {
          class: `sort-mark${active ? " is-active" : ""}`,
          text: active ? (app.sort.direction === "asc" ? "▲" : "▼") : "↕",
        }),
      );
      th.setAttribute(
        "aria-sort",
        active ? (app.sort.direction === "asc" ? "ascending" : "descending") : "none",
      );
    }
    th.append(inner);
    headRow.append(th);
  }
  dom.tableHead.replaceChildren(headRow);
}

function emptyRow(columnCount) {
  const row = h("tr", { class: "empty-row" });
  const cell = h("td", { colSpan: columnCount });
  let state;
  if (app.loadError) {
    state = h(
      "div",
      { class: "empty-state" },
      h(
        "span",
        { class: "empty-icon" },
        icon(
          '<circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M10 6.4v4.2M10 13.4v.2" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>',
        ),
      ),
      h("span", { class: "empty-title", text: "无法读取工作区" }),
      h("span", { class: "empty-copy", text: app.loadError }),
      h(
        "button",
        {
          class: "button button-quiet button-small",
          type: "button",
          onclick: () => loadState(),
        },
        "重新加载",
      ),
    );
  } else if (!app.items.length) {
    state = h(
      "div",
      { class: "empty-state" },
      h(
        "span",
        { class: "empty-icon" },
        icon(
          '<path d="M4 5.5h12v9H4zM7 8.5h6M7 11.5h4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>',
        ),
      ),
      h("span", { class: "empty-title", text: "还没有同步到任何记录" }),
      h("span", { class: "empty-copy", text: "同步后，Issue 与 Pull Request 会出现在这里。" }),
      h(
        "button",
        {
          class: "button button-primary button-small",
          type: "button",
          dataset: { action: "sync" },
        },
        "同步 GitHub",
      ),
    );
  } else {
    state = h(
      "div",
      { class: "empty-state" },
      h(
        "span",
        { class: "empty-icon" },
        icon(
          '<circle cx="9" cy="9" r="4.8" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="m12.7 12.7 3.6 3.6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>',
        ),
      ),
      h("span", { class: "empty-title", text: "没有符合条件的记录" }),
      h("span", { class: "empty-copy", text: "试试调整搜索词或清除筛选条件。" }),
      h(
        "button",
        {
          class: "button button-quiet button-small",
          type: "button",
          dataset: { action: "reset" },
        },
        "清除筛选",
      ),
    );
  }
  cell.append(state);
  row.append(cell);
  return row;
}

function renderTableBody(items, columnCount) {
  const fragment = document.createDocumentFragment();
  if (!items.length) {
    fragment.append(emptyRow(columnCount));
    dom.tableBody.replaceChildren(fragment);
    return;
  }
  for (const item of items) {
    const row = h("tr", { dataset: { itemId: item.id } });
    row.append(
      h(
        "td",
        { class: "col-title" },
        h(
          "div",
          { class: "item-main" },
          h("a", {
            class: "item-link",
            href: safeURL(item.url),
            target: "_blank",
            rel: "noopener noreferrer",
            text: item.title,
            title: item.title,
          }),
        ),
        h(
          "div",
          { class: "item-meta" },
          h("span", { class: "repo", text: item.repo, title: item.repo }),
          h("span", { class: "number", text: `#${item.number}` }),
          item.comments
            ? [
                h("span", { class: "dot-sep" }),
                h("span", { text: `${item.comments} 条评论` }),
              ]
            : null,
        ),
      ),
    );
    const kindCell = h("td", { class: "col-kind" });
    kindCell.append(
      h("div", { class: "chip-row" }, [
        h("span", {
          class: `badge ${item.kind === "pr" ? "badge-pr" : "badge-issue"}`,
          text: item.kind === "pr" ? "Pull Request" : "Issue",
        }),
        item.is_draft
          ? h("span", { class: "badge badge-draft", text: "草稿" })
          : null,
      ]),
    );
    row.append(kindCell);
    const statusCell = h("td", { class: "col-state" });
    statusCell.append(stateCell(item));
    row.append(statusCell);

    const tagsCell = h("td", { class: "col-tags" });
    tagsCell.append(tagCellContent(item));
    row.append(tagsCell);

    const authorCell = h("td", { class: "col-author" });
    authorCell.append(person(item.author));
    row.append(authorCell);

    const labelsCell = h("td", { class: "col-labels" });
    const labels = currentLabels(item);
    if (!labels.length) {
      labelsCell.append(fallback("—"));
    } else {
      const rowWrap = h("div", { class: "chip-row" });
      labels.slice(0, 2).forEach((label) => rowWrap.append(labelChip(label)));
      if (labels.length > 2) {
        rowWrap.append(
          h("span", {
            class: "tag-more",
            text: `+${labels.length - 2}`,
            title: labels.map((label) => label.name).join("、"),
          }),
        );
      }
      labelsCell.append(rowWrap);
    }
    row.append(labelsCell);

    const assigneesCell = h("td", { class: "col-assignees" });
    const assignees = currentAssignees(item);
    if (!assignees.length) {
      assigneesCell.append(fallback("—"));
    } else {
      const list = h("div", { class: "assignee-list" });
      assignees.slice(0, 2).forEach((login) => list.append(assignee(login)));
      if (assignees.length > 2) {
        list.append(
          h("span", {
            class: "tag-more",
            text: `+${assignees.length - 2}`,
            title: assignees.map((login) => `@${login}`).join("、"),
          }),
        );
      }
      assigneesCell.append(list);
    }
    row.append(assigneesCell);

    const updated = h("td", { class: "col-updated" });
    updated.append(updatedCell(item));
    row.append(updated);

    const stale = h("td", { class: "col-stale" });
    stale.append(staleCell(item));
    row.append(stale);

    for (const field of app.fields) {
      const cell = h("td", { class: "col-field" });
      cell.append(fieldCell(item, field));
      row.append(cell);
    }
    fragment.append(row);
  }
  dom.tableBody.replaceChildren(fragment);
}

function renderTable() {
  const allColumns = columns();
  renderTableHead();
  const filtered = sortedItems(applyFilters());
  const scrollTop = dom.tableScroller.scrollTop;
  const scrollLeft = dom.tableScroller.scrollLeft;
  renderTableBody(filtered, allColumns.length);
  dom.tableScroller.scrollTop = scrollTop;
  dom.tableScroller.scrollLeft = scrollLeft;
  dom.resultCount.textContent =
    filtered.length === app.items.length
      ? `${filtered.length} 条记录`
      : `显示 ${filtered.length} / ${app.items.length} 条`;
  dom.resetFilters.hidden = !hasFilters();
}

function renderWorkspaceMeta() {
  const workspace = app.data?.workspace || {};
  const parts = [
    `${workspace.repo_count || 0} 个仓库`,
    `${app.items.length} 条记录`,
  ];
  if (workspace.stale_days) parts.push(`超过 ${workspace.stale_days} 天未更新视为停滞`);
  dom.workspaceMeta.textContent = parts.join(" · ");
  dom.workspaceMeta.title = [
    workspace.config_path ? `配置：${workspace.config_path}` : "",
    workspace.db_path ? `数据库：${workspace.db_path}` : "",
  ]
    .filter(Boolean)
    .join("\n");
}

function statIsActive(key) {
  if (key === "total") return !hasFilters();
  if (key === "open") return app.filters.state === "open";
  if (key === "closed") return app.filters.state === "closed";
  if (key === "issues") return app.filters.kind === "issue";
  if (key === "prs") return app.filters.kind === "pr";
  if (key === "stale") return app.filters.stale;
  return false;
}

function renderStats() {
  const stats = app.data?.stats || {
    total: 0,
    open: 0,
    closed: 0,
    issues: 0,
    prs: 0,
    stale: 0,
    review_stale: 0,
  };
  const cards = [
    {
      key: "total",
      label: "全部事项",
      value: stats.total,
      sub: `${stats.open} 个打开中`,
      tone: "gray",
    },
    {
      key: "open",
      label: "打开中",
      value: stats.open,
      sub: `${stats.issues} Issue · ${stats.prs} PR`,
      tone: "green",
    },
    {
      key: "closed",
      label: "已关闭 / 合并",
      value: stats.closed,
      sub: "同步窗口内",
      tone: "blue",
    },
    {
      key: "issues",
      label: "Issue",
      value: stats.issues,
      sub: "GitHub 原始类型",
      tone: "purple",
    },
    {
      key: "prs",
      label: "Pull Request",
      value: stats.prs,
      sub: `${stats.review_stale} 个评审停滞`,
      tone: "blue",
    },
    {
      key: "stale",
      label: "需要关注",
      value: stats.stale,
      sub: `${stats.review_stale} 个评审超时`,
      tone: "orange",
    },
  ];
  dom.statsGrid.replaceChildren(
    ...cards.map((card) =>
      h(
        "button",
        {
          class: `stat-card tone-${card.tone}${statIsActive(card.key) ? " is-active" : ""}`,
          type: "button",
          dataset: { stat: card.key },
          title: statTitle(card.key),
        },
        h("span", { class: "stat-label", text: card.label }),
        h("strong", { class: "stat-value", text: card.value }),
        h("span", { class: "stat-sub", text: card.sub }),
      ),
    ),
  );
}

function statTitle(key) {
  const titles = {
    total: "查看全部记录",
    open: "筛选中打开的事项",
    closed: "筛选已关闭或已合并的事项",
    issues: "只看 Issue",
    prs: "只看 Pull Request",
    stale: "筛选停滞或评审超时的事项",
  };
  return titles[key] || "";
}

function renderFilterOptions() {
  dom.stateFilter.value = app.filters.state;
  dom.kindFilter.value = app.filters.kind;
  dom.staleFilter.checked = app.filters.stale;
  const options = [h("option", { value: "", text: "全部" })];
  for (const tag of app.tags) {
    options.push(
      h("option", {
        value: String(tag.id),
        text: `${tag.name}（${tag.count}）`,
      }),
    );
  }
  dom.tagFilter.replaceChildren(...options);
  dom.tagFilter.value = app.filters.tag;
  if (!app.tags.some((tag) => String(tag.id) === app.filters.tag)) {
    app.filters.tag = "";
    dom.tagFilter.value = "";
  }

  const repoNames = new Set(app.repositories.map((repo) => repo.name.toLowerCase()));
  app.filters.repos = app.filters.repos.filter((repo) =>
    repoNames.has(String(repo).toLowerCase()),
  );
  const selectedCount = app.filters.repos.length;
  dom.repoFilterButton.classList.toggle("is-active", selectedCount > 0);
  dom.repoFilterLabel.textContent =
    selectedCount === 1 ? app.filters.repos[0] : "仓库";
  dom.repoFilterCount.hidden = selectedCount === 0;
  dom.repoFilterCount.textContent = String(selectedCount);
  dom.repoFilterButton.title =
    selectedCount === 0
      ? "按仓库筛选，可多选"
      : `已选择 ${selectedCount} 个仓库`;
}

function renderFooter() {
  const run = app.data?.last_sync;
  if (!run) {
    dom.lastSyncDetail.textContent = "尚未同步";
    dom.lastSyncDetail.title = "";
    return;
  }
  const errors = (app.data.repo_statuses || []).filter((repo) => repo.last_error);
  let text = `上次同步 ${formatDateTime(run.finished_at || run.started_at)} · ${run.repo_count} 个仓库 / ${run.item_count} 条`;
  if (run.status === "partial" || run.status === "error") {
    text += ` · ${errors.length || 1} 个仓库异常`;
  }
  dom.lastSyncDetail.textContent = text;
  dom.lastSyncDetail.title = [run.error, ...errors.map((repo) => `${repo.full_name}: ${repo.last_error}`)]
    .filter(Boolean)
    .join("\n");
}

function renderSyncUI(status = null) {
  const state = status || app.data?.sync_status || {};
  const lastSync = app.data?.last_sync;
  const running = Boolean(state.running ?? app.data?.syncing);
  dom.syncButton.disabled = running;
  updateRepoManagementState(running);
  const label = dom.syncButton.querySelector("span");
  if (label) label.textContent = running ? "同步中…" : "同步 GitHub";
  dom.syncDot.className = "sync-dot";
  if (running) {
    dom.syncDot.classList.add("is-running");
    dom.syncText.textContent = state.message ? `${state.message}…` : "正在同步 GitHub…";
    dom.syncState.title = state.message || "同步进行中";
    return;
  }
  if (state.error || lastSync?.status === "error" || lastSync?.status === "partial") {
    dom.syncDot.classList.add("is-error");
    dom.syncText.textContent = "上次同步有异常";
    dom.syncState.title = state.error || lastSync?.error || "部分仓库同步失败";
    return;
  }
  if (lastSync) {
    dom.syncDot.classList.add("is-success");
    dom.syncText.textContent = `同步于 ${formatRelative(lastSync.finished_at || lastSync.started_at)}`;
    dom.syncState.title = `上次同步：${formatDateTime(lastSync.finished_at || lastSync.started_at)}`;
    return;
  }
  dom.syncText.textContent = "尚未同步";
  dom.syncState.title = "点击右上角开始同步";
}

function applyState(data) {
  app.data = data;
  app.items = Array.isArray(data.items) ? data.items : [];
  app.tags = Array.isArray(data.tags) ? data.tags : [];
  app.fields = Array.isArray(data.fields) ? data.fields : [];
  app.repositories = Array.isArray(data.repositories) ? data.repositories : [];
  app.loadError = "";
  if (
    app.filters.tag &&
    !app.tags.some((tag) => String(tag.id) === app.filters.tag)
  ) {
    app.filters.tag = "";
  }
  if (app.filters.repos.length) {
    app.filters.repos = app.filters.repos.filter((repo) =>
      app.repositories.some((candidate) => sameRepo(candidate.name, repo)),
    );
  }
  renderWorkspaceMeta();
  renderStats();
  renderFilterOptions();
  renderTable();
  renderFooter();
  renderTagManageList();
  renderFieldManageList();
  renderRepoManageList();
  renderSyncUI();
  if (data.syncing) startPolling();
}

async function loadState({ silent = false } = {}) {
  try {
    const data = await api("/api/state");
    applyState(data);
  } catch (error) {
    app.loadError = error.message;
    if (!silent) toast(error.message);
    dom.syncDot.className = "sync-dot is-error";
    dom.syncText.textContent = "无法读取工作区";
    renderTable();
  }
}

function openPopover(anchor, content, { width = 320 } = {}) {
  closePopover();
  const popover = dom.popover;
  const actualWidth = Math.min(width, window.innerWidth - 24);
  popover.hidden = false;
  popover.replaceChildren(content);
  popover.style.width = `${actualWidth}px`;
  popover.style.left = "0px";
  popover.style.top = "0px";
  const rect = anchor.getBoundingClientRect();
  const popoverRect = popover.getBoundingClientRect();
  let left = Math.min(Math.max(12, rect.left), window.innerWidth - actualWidth - 12);
  if (rect.left + actualWidth > window.innerWidth - 12) {
    left = Math.max(12, rect.right - actualWidth);
  }
  let top = rect.bottom + 7;
  if (top + popoverRect.height > window.innerHeight - 12) {
    top = Math.max(12, rect.top - popoverRect.height - 7);
  }
  popover.style.left = `${Math.round(left)}px`;
  popover.style.top = `${Math.round(top)}px`;
  return popover;
}

function closePopover() {
  dom.popover.hidden = true;
  dom.popover.replaceChildren();
  if (dom.repoFilterButton) {
    dom.repoFilterButton.setAttribute("aria-expanded", "false");
  }
  if (popoverCleanup) {
    popoverCleanup();
    popoverCleanup = null;
  }
}

function openRepoFilter(anchor) {
  let query = "";
  const search = h("input", {
    class: "popover-search",
    type: "search",
    placeholder: "搜索仓库",
    autocomplete: "off",
  });
  const list = h("div", { class: "picker-list repo-picker-list" });
  const selectedCount = h("small", { text: "" });
  const recordCount = h("span", { text: "" });
  const content = h(
    "div",
    { class: "field-editor" },
    h(
      "div",
      { class: "popover-title" },
      h("span", { text: "按仓库筛选" }),
      selectedCount,
    ),
    search,
    h(
      "div",
      { class: "popover-bulk-actions" },
      h("button", {
        class: "link-button",
        type: "button",
        text: "全选",
        onclick: () => {
          app.filters.repos = app.repositories.map((repo) => repo.name);
          updateRepoFilter();
        },
      }),
      h("span", { class: "popover-action-divider", text: "·" }),
      h("button", {
        class: "link-button",
        type: "button",
        text: "清空",
        onclick: () => {
          app.filters.repos = [];
          updateRepoFilter();
        },
      }),
    ),
    list,
    h(
      "div",
      { class: "popover-footer" },
      recordCount,
      h("button", {
        class: "link-button",
        type: "button",
        text: "管理仓库",
        onclick: () => {
          closePopover();
          openReposDialog();
        },
      }),
    ),
  );
  openPopover(anchor, content, { width: 340 });
  anchor.setAttribute("aria-expanded", "true");
  setTimeout(() => search.focus(), 0);

  function updateRepoFilter() {
    renderFilterOptions();
    renderStats();
    renderTable();
    draw();
  }

  function draw() {
    const lower = query.trim().toLocaleLowerCase("zh-CN");
    const repos = app.repositories
      .filter((repo) => !lower || repo.name.toLocaleLowerCase("zh-CN").includes(lower))
      .sort((left, right) => {
        const leftSelected = app.filters.repos.some((repo) => sameRepo(repo, left.name));
        const rightSelected = app.filters.repos.some((repo) => sameRepo(repo, right.name));
        if (leftSelected !== rightSelected) return leftSelected ? -1 : 1;
        return left.name.localeCompare(right.name, "zh-CN");
      });
    selectedCount.textContent = `${app.filters.repos.length} 个已选`;
    recordCount.textContent = `${applyFilters().length} 条匹配记录`;
    if (!repos.length) {
      list.replaceChildren(
        h("div", {
          class: "popover-empty",
          text: app.repositories.length ? "没有匹配的仓库" : "还没有追踪的仓库",
        }),
      );
      return;
    }
    list.replaceChildren(
      ...repos.map((repo) => {
        const checked = app.filters.repos.some((selected) =>
          sameRepo(selected, repo.name),
        );
        const checkbox = h("input", { type: "checkbox", checked });
        checkbox.addEventListener("change", () => {
          if (checkbox.checked) {
            if (!app.filters.repos.some((selected) => sameRepo(selected, repo.name))) {
              app.filters.repos.push(repo.name);
            }
          } else {
            app.filters.repos = app.filters.repos.filter(
              (selected) => !sameRepo(selected, repo.name),
            );
          }
          updateRepoFilter();
        });
        const status = repoStatus(repo.name);
        return h(
          "label",
          { class: "picker-row repo-picker-row" },
          checkbox,
          h(
            "span",
            { class: "repo-picker-copy" },
            h("span", { class: "picker-name", text: repo.name, title: repo.name }),
            h("span", {
              class: "repo-picker-branch",
              text: status?.tracked_branch
                ? `分支 ${status.tracked_branch}`
                : repo.branch
                  ? `分支 ${repo.branch}`
                  : "默认分支",
            }),
          ),
          h("span", { class: "picker-count", text: String(repoItemCount(repo.name)) }),
        );
      }),
    );
  }

  search.addEventListener("input", () => {
    query = search.value;
    draw();
  });
  popoverCleanup = () => {};
  draw();
}

function applyTagCountDelta(beforeIDs, afterIDs) {
  const before = new Set(beforeIDs);
  const after = new Set(afterIDs);
  for (const tag of app.tags) {
    if (before.has(tag.id) && !after.has(tag.id)) {
      tag.count = Math.max(0, (tag.count || 0) - 1);
    } else if (!before.has(tag.id) && after.has(tag.id)) {
      tag.count = (tag.count || 0) + 1;
    }
  }
}

function updateTagCell(item) {
  const row = dom.tableBody.querySelector(`tr[data-item-id="${item.id}"]`);
  if (!row) return;
  const cell = row.querySelector("td.col-tags");
  if (!cell) return;
  cell.replaceChildren(tagCellContent(item));
}

async function saveItemTags(item, selected) {
  const before = currentTags(item).map((tag) => tag.id);
  const response = await api(`/api/items/${item.id}/tags`, {
    method: "PUT",
    body: { tag_ids: [...selected] },
  });
  const tags = Array.isArray(response.tags) ? response.tags : [];
  applyTagCountDelta(
    before,
    tags.map((tag) => tag.id),
  );
  item.tags = tags;
  selected.clear();
  tags.forEach((tag) => selected.add(tag.id));
  renderFilterOptions();
  updateTagCell(item);
  return tags;
}

function openTagPicker(anchor, item) {
  const selected = new Set(currentTags(item).map((tag) => tag.id));
  let query = "";
  let busy = false;
  const search = h("input", {
    class: "popover-search",
    type: "search",
    placeholder: "搜索或创建标签",
    autocomplete: "off",
  });
  const list = h("div", { class: "picker-list" });
  const footer = h(
    "div",
    { class: "popover-footer" },
    h("span", { text: "标签仅保存在本机" }),
    h("button", {
      class: "link-button",
      type: "button",
      text: "管理标签",
      onclick: () => {
        closePopover();
        openTagsDialog();
      },
    }),
  );
  const content = h(
    "div",
    { class: "field-editor" },
    h(
      "div",
      { class: "popover-title" },
      h("span", { text: "本地标签" }),
      h("small", { text: `${selected.size} 个已选` }),
    ),
    search,
    list,
    footer,
  );
  openPopover(anchor, content, { width: 300 });
  setTimeout(() => search.focus(), 0);

  function draw() {
    const lower = query.trim().toLocaleLowerCase("zh-CN");
    const exact = app.tags.find(
      (tag) => tag.name.toLocaleLowerCase("zh-CN") === lower,
    );
    const tags = app.tags
      .filter((tag) => !lower || tag.name.toLocaleLowerCase("zh-CN").includes(lower))
      .sort((left, right) => {
        const leftSelected = selected.has(left.id) ? 0 : 1;
        const rightSelected = selected.has(right.id) ? 0 : 1;
        if (leftSelected !== rightSelected) return leftSelected - rightSelected;
        return left.name.localeCompare(right.name, "zh-CN");
      });
    const fragment = document.createDocumentFragment();
    if (lower && !exact) {
      const create = h(
        "button",
        {
          class: "picker-create",
          type: "button",
          disabled: busy,
        },
        h("span", { text: "+" }),
        h("span", { text: `创建标签「${query.trim()}」` }),
      );
      create.addEventListener("click", async () => {
        if (busy) return;
        busy = true;
        draw();
        try {
          const tag = await api("/api/tags", {
            method: "POST",
            body: { name: query.trim() },
          });
          tag.count = 0;
          app.tags.push(tag);
          selected.add(tag.id);
          query = "";
          search.value = "";
          await saveItemTags(item, selected);
          toast(`已创建标签「${tag.name}」`, "success");
        } catch (error) {
          toast(error.message);
        } finally {
          busy = false;
          draw();
        }
      });
      fragment.append(create);
    }
    if (!tags.length) {
      if (!lower || exact) {
        fragment.append(
          h("div", {
            class: "popover-empty",
            text: app.tags.length ? "没有匹配的标签" : "还没有标签，直接输入名称创建。",
          }),
        );
      }
    } else {
      tags.forEach((tag) => {
        const checkbox = h("input", {
          type: "checkbox",
          checked: selected.has(tag.id),
          disabled: busy,
        });
        checkbox.addEventListener("change", async () => {
          if (busy) return;
          busy = true;
          if (checkbox.checked) selected.add(tag.id);
          else selected.delete(tag.id);
          draw();
          try {
            await saveItemTags(item, selected);
          } catch (error) {
            if (checkbox.checked) selected.delete(tag.id);
            else selected.add(tag.id);
            toast(error.message);
          } finally {
            busy = false;
            draw();
          }
        });
        fragment.append(
          h(
            "label",
            { class: "picker-row" },
            checkbox,
            h("span", {
              class: "label-dot",
              style: { background: normalizeColor(tag.color) },
            }),
            h("span", { class: "picker-name", text: tag.name, title: tag.name }),
            h("span", { class: "picker-count", text: String(tag.count || 0) }),
          ),
        );
      });
    }
    list.replaceChildren(fragment);
    const titleCount = content.querySelector(".popover-title small");
    if (titleCount) titleCount.textContent = `${selected.size} 个已选`;
  }

  search.addEventListener("input", () => {
    query = search.value;
    draw();
  });
  popoverCleanup = () => {};
  draw();
}

function toggleCheckboxField(item, field, button) {
  const key = String(field.id);
  const next = !(item.fields[key] === true);
  button.disabled = true;
  api(`/api/items/${item.id}/fields/${field.id}`, {
    method: "PUT",
    body: { value: next },
  })
    .then(() => {
      item.fields[key] = next;
      renderTable();
    })
    .catch((error) => {
      button.disabled = false;
      toast(error.message);
    });
}

function openFieldEditor(anchor, item, field) {
  const key = String(field.id);
  const current = item.fields?.[key];
  let saving = false;
  let readValue = () => null;

  const editor = h("div", { class: "field-editor" });
  editor.append(
    h(
      "div",
      { class: "popover-title" },
      h("span", { text: field.name }),
      h("small", { text: fieldTypeLabel(field.type) }),
    ),
  );
  const body = h("div", { class: "field-editor" });
  const actions = h("div", { class: "editor-actions" });
  const saveButton = h("button", {
    class: "button button-small button-primary",
    type: "button",
    text: "保存",
  });
  const cancelButton = h("button", {
    class: "button button-small button-quiet",
    type: "button",
    text: "取消",
    onclick: closePopover,
  });
  const clearButton = h("button", {
    class: "button button-small button-quiet",
    type: "button",
    text: "清除",
  });

  if (field.type === "long_text") {
    const textarea = h("textarea", {
      class: "editor-textarea",
      rows: 4,
      placeholder: "填写内容",
    });
    textarea.value = current == null ? "" : String(current);
    body.append(textarea, h("div", { class: "editor-hint", text: "⌘/Ctrl + Enter 保存" }));
    readValue = () => {
      const value = textarea.value.trim();
      return value || null;
    };
    textarea.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        save(readValue());
      }
    });
    setTimeout(() => textarea.focus(), 0);
  } else if (field.type === "select") {
    const select = h("select", { class: "editor-select" });
    select.append(h("option", { value: "", text: "未选择" }));
    for (const option of field.options || []) {
      select.append(h("option", { value: option, text: option }));
    }
    select.value = current == null ? "" : String(current);
    readValue = () => select.value || null;
    body.append(select);
  } else if (field.type === "multi_select") {
    const options = h("div", { class: "multi-options" });
    const chosen = new Set(Array.isArray(current) ? current : []);
    for (const option of field.options || []) {
      const checkbox = h("input", {
        type: "checkbox",
        checked: chosen.has(option),
      });
      checkbox.addEventListener("change", () => {
        if (checkbox.checked) chosen.add(option);
        else chosen.delete(option);
      });
      options.append(
        h("label", { class: "multi-option" }, checkbox, h("span", { text: option })),
      );
    }
    readValue = () => (chosen.size ? [...chosen] : null);
    body.append(options);
  } else if (field.type === "date") {
    const input = h("input", { class: "editor-input", type: "date" });
    input.value = current == null ? "" : String(current);
    readValue = () => input.value || null;
    body.append(input);
    setTimeout(() => input.focus(), 0);
  } else if (field.type === "checkbox") {
    const input = h("input", { type: "checkbox", checked: current === true });
    readValue = () => input.checked;
    body.append(
      h("label", { class: "multi-option" }, input, h("span", { text: "已勾选" })),
    );
  } else {
    const input = h("input", {
      class: "editor-input",
      type: field.type === "url" ? "url" : field.type === "number" ? "number" : "text",
      step: field.type === "number" ? "any" : null,
      placeholder: field.type === "url" ? "https://" : "填写内容",
    });
    input.value = current == null ? "" : String(current);
    readValue = () => {
      const value = input.value.trim();
      if (!value) return null;
      if (field.type === "number") {
        const number = Number(value);
        if (!Number.isFinite(number)) throw new Error("请输入有效数字");
        return number;
      }
      return value;
    };
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        save(readValue());
      }
    });
    body.append(input);
    setTimeout(() => input.focus(), 0);
  }

  const hasValue = !isEmptyValue(current);
  if (hasValue) actions.append(clearButton);
  actions.append(h("span", { class: "toolbar-spacer" }), cancelButton, saveButton);
  editor.append(body, actions);
  openPopover(anchor, editor, {
    width: field.type === "long_text" || field.type === "multi_select" ? 340 : 300,
  });

  async function save(value) {
    if (saving) return;
    saving = true;
    saveButton.disabled = true;
    try {
      const response = await api(`/api/items/${item.id}/fields/${field.id}`, {
        method: "PUT",
        body: { value },
      });
      if (response.value == null) delete item.fields[key];
      else item.fields[key] = response.value;
      closePopover();
      renderTable();
    } catch (error) {
      toast(error.message);
    } finally {
      saving = false;
      saveButton.disabled = false;
    }
  }

  saveButton.addEventListener("click", () => {
    try {
      save(readValue());
    } catch (error) {
      toast(error.message);
    }
  });
  clearButton.addEventListener("click", () => save(null));
}

function fieldTypeLabel(type) {
  const labels = {
    text: "文本",
    long_text: "长文本",
    number: "数字",
    select: "单选",
    multi_select: "多选",
    date: "日期",
    checkbox: "复选框",
    url: "URL",
  };
  return labels[type] || type;
}

async function startSync() {
  if (dom.syncButton.disabled) return;
  dom.syncButton.disabled = true;
  dom.syncDot.className = "sync-dot is-running";
  dom.syncText.textContent = "正在启动同步…";
  try {
    await api("/api/sync", { method: "POST", body: {} });
    toast("已开始同步 GitHub", "success");
    startPolling();
  } catch (error) {
    toast(error.message);
    renderSyncUI();
  }
}

function startPolling() {
  if (app.pollTimer) return;
  const tick = async () => {
    try {
      const status = await api("/api/sync/status");
      renderSyncUI(status);
      if (!status.running) {
        stopPolling();
        await loadState({ silent: true });
        if (status.error) toast(`同步未完全成功：${shortError(status.error)}`);
        else toast("同步完成", "success");
      }
    } catch (error) {
      stopPolling();
      toast(`无法获取同步状态：${error.message}`);
    }
  };
  app.pollTimer = setInterval(tick, 1500);
  setTimeout(tick, 300);
}

function stopPolling() {
  if (!app.pollTimer) return;
  clearInterval(app.pollTimer);
  app.pollTimer = null;
}

function shortError(message) {
  const text = String(message || "").split("\n")[0];
  return text.length > 180 ? `${text.slice(0, 180)}…` : text;
}

function openTagsDialog() {
  renderTagManageList();
  if (!dom.tagsDialog.open) dom.tagsDialog.showModal();
}

function renderTagManageList() {
  if (!dom.tagManageList) return;
  if (!app.tags.length) {
    dom.tagManageList.replaceChildren(
      h("div", { class: "popover-empty", text: "还没有标签。" }),
    );
    return;
  }
  const rows = app.tags.map((tag) => {
    const color = h("input", { type: "color", value: normalizeColor(tag.color) });
    const name = h("input", {
      class: "row-name",
      type: "text",
      maxLength: 24,
      value: tag.name,
    });
    const preview = h("span", { class: "row-meta", text: `${tag.count} 条记录` });
    const save = h("button", {
      class: "button button-small button-quiet",
      type: "button",
      text: "保存",
    });
    const remove = h("button", {
      class: "button button-small button-danger",
      type: "button",
      text: "删除",
    });
    save.addEventListener("click", async () => {
      save.disabled = true;
      try {
        await api(`/api/tags/${tag.id}`, {
          method: "PATCH",
          body: { name: name.value, color: color.value },
        });
        await loadState({ silent: true });
        toast("标签已更新", "success");
      } catch (error) {
        toast(error.message);
      } finally {
        save.disabled = false;
      }
    });
    remove.addEventListener("click", async () => {
      if (!window.confirm(`删除标签「${tag.name}」？关联记录不会被删除。`)) return;
      remove.disabled = true;
      try {
        await api(`/api/tags/${tag.id}`, { method: "DELETE" });
        await loadState({ silent: true });
        toast("标签已删除", "success");
      } catch (error) {
        toast(error.message);
      } finally {
        remove.disabled = false;
      }
    });
    color.addEventListener("input", () => {
      preview.textContent = `${tag.count} 条记录 · ${color.value.toUpperCase()}`;
    });
    return h(
      "div",
      { class: "dialog-row" },
      h("label", { class: "color-field", title: "标签颜色" }, color),
      name,
      preview,
      h("div", { class: "row-actions" }, save, remove),
    );
  });
  dom.tagManageList.replaceChildren(...rows);
}

function openFieldsDialog() {
  renderFieldManageList();
  if (!dom.fieldsDialog.open) dom.fieldsDialog.showModal();
}

function parseOptions(value) {
  const seen = new Set();
  const options = [];
  for (const item of String(value || "").split(/[,，\n]/)) {
    const option = item.trim();
    if (!option) continue;
    const key = option.toLocaleLowerCase("zh-CN");
    if (seen.has(key)) continue;
    seen.add(key);
    options.push(option);
  }
  return options;
}

function renderFieldManageList() {
  if (!dom.fieldManageList) return;
  if (!app.fields.length) {
    dom.fieldManageList.replaceChildren(
      h("div", {
        class: "popover-empty",
        text: "还没有自定义字段。创建后会作为新列出现在表格中。",
      }),
    );
    return;
  }
  const rows = app.fields.map((field) => {
    const name = h("input", {
      class: "row-name",
      type: "text",
      maxLength: 60,
      value: field.name,
    });
    const selectType = field.type === "select" || field.type === "multi_select";
    const options = selectType
      ? h("input", {
          class: "field-edit-options",
          type: "text",
          value: (field.options || []).join(", "),
          title: "选项使用逗号分隔",
        })
      : h("span", { class: "row-meta", text: fieldTypeLabel(field.type) });
    const save = h("button", {
      class: "button button-small button-quiet",
      type: "button",
      text: "保存",
    });
    const remove = h("button", {
      class: "button button-small button-danger",
      type: "button",
      text: "删除",
    });
    save.addEventListener("click", async () => {
      const payload = { name: name.value };
      if (selectType) {
        payload.options = parseOptions(options.value);
        if (!payload.options.length) {
          toast("单选或多选字段至少需要一个选项");
          return;
        }
      }
      save.disabled = true;
      try {
        await api(`/api/fields/${field.id}`, { method: "PATCH", body: payload });
        await loadState({ silent: true });
        toast("字段已更新", "success");
      } catch (error) {
        toast(error.message);
      } finally {
        save.disabled = false;
      }
    });
    remove.addEventListener("click", async () => {
      if (
        !window.confirm(
          `删除字段「${field.name}」？该列已填写的内容也会一并删除。`,
        )
      ) {
        return;
      }
      remove.disabled = true;
      try {
        await api(`/api/fields/${field.id}`, { method: "DELETE" });
        await loadState({ silent: true });
        toast("字段已删除", "success");
      } catch (error) {
        toast(error.message);
      } finally {
        remove.disabled = false;
      }
    });
    return h(
      "div",
      { class: "dialog-row" },
      name,
      options,
      h("span", { class: "row-meta", text: fieldTypeLabel(field.type) }),
      h("div", { class: "row-actions" }, save, remove),
    );
  });
  dom.fieldManageList.replaceChildren(...rows);
}

function openReposDialog() {
  renderRepoManageList();
  if (!dom.reposDialog.open) dom.reposDialog.showModal();
}

function updateRepoManagementState(running = syncIsRunning()) {
  if (!dom.manageRepos) return;
  dom.manageRepos.disabled = running;
  dom.manageRepos.title = running ? "同步进行中，暂时不能修改仓库" : "添加、删除或切换追踪分支";
  if (dom.repoCreateForm) {
    for (const control of dom.repoCreateForm.querySelectorAll("input, button")) {
      control.disabled = running;
    }
  }
  if (dom.repoManageList) {
    for (const control of dom.repoManageList.querySelectorAll("input, button")) {
      control.disabled = running;
    }
  }
}

function renderRepoManageList() {
  if (!dom.repoManageList) return;
  const running = syncIsRunning();
  if (!app.repositories.length) {
    dom.repoManageList.replaceChildren(
      h("div", {
        class: "popover-empty",
        text: "还没有追踪的仓库。在上方输入 owner/repo 后添加。",
      }),
    );
    updateRepoManagementState(running);
    return;
  }

  const rows = app.repositories.map((repo) => {
    const status = repoStatus(repo.name) || {};
    const branch = h("input", {
      class: "repo-branch-input",
      type: "text",
      value: repo.branch || "",
      placeholder: "留空跟随默认分支",
      autocomplete: "off",
      disabled: running,
    });
    const save = h("button", {
      class: "button button-small button-quiet",
      type: "button",
      text: "保存分支",
      disabled: running,
    });
    const remove = h("button", {
      class: "button button-small button-danger",
      type: "button",
      text: "删除",
      disabled: running,
    });

    save.addEventListener("click", async () => {
      save.disabled = true;
      try {
        await api("/api/repos", {
          method: "PATCH",
          body: { repo: repo.name, branch: branch.value.trim() },
        });
        await loadState({ silent: true });
        toast("追踪分支已更新", "success");
      } catch (error) {
        toast(error.message);
        save.disabled = false;
      }
    });

    remove.addEventListener("click", async () => {
      if (
        !window.confirm(
          `删除仓库「${repo.name}」？本地已同步的 Issue、PR 和标注也会一并清理。`,
        )
      ) {
        return;
      }
      remove.disabled = true;
      try {
        await api(`/api/repos?repo=${encodeURIComponent(repo.name)}`, {
          method: "DELETE",
        });
        await loadState({ silent: true });
        toast("仓库已删除", "success");
      } catch (error) {
        toast(error.message);
        remove.disabled = false;
      }
    });

    const statusLabel = status.last_error
      ? "同步异常"
      : status.last_synced_at
        ? "已同步"
        : "尚未同步";
    const statusTone = status.last_error
      ? "is-error"
      : status.last_synced_at
        ? "is-success"
        : "is-idle";
    const actualBranch =
      status.tracked_branch || repo.branch || "默认分支";
    const commit = status.commit_sha
      ? h(
          "div",
          { class: "repo-commit" },
          h("span", {
            class: "repo-commit-message",
            text: status.commit_message || "（无提交标题）",
            title: status.commit_message || "",
          }),
          h(
            "a",
            {
              class: "repo-commit-link",
              href: `https://github.com/${repo.name}/commit/${status.commit_sha}`,
              target: "_blank",
              rel: "noreferrer",
              text: status.commit_sha.slice(0, 8),
            },
          ),
          status.commit_committed_at
            ? h("span", { text: formatShortDate(status.commit_committed_at) })
            : null,
        )
      : h("div", { class: "repo-commit is-empty", text: "尚未获取到最新提交" });

    return h(
      "div",
      { class: "repo-manage-row" },
      h(
        "div",
        { class: "repo-manage-main" },
        h(
          "div",
          { class: "repo-manage-heading" },
          h(
            "a",
            {
              class: "repo-manage-name",
              href: `https://github.com/${repo.name}`,
              target: "_blank",
              rel: "noreferrer",
              text: repo.name,
            },
          ),
          h("span", {
            class: `repo-status ${statusTone}`,
            text: statusLabel,
          }),
        ),
        commit,
        h(
          "div",
          { class: "repo-manage-meta" },
          h("span", { text: `实际追踪：${actualBranch}` }),
          h("span", { text: `${repoItemCount(repo.name)} 条本地记录` }),
          status.last_synced_at
            ? h("span", {
                text: `最近同步：${formatDateTime(status.last_synced_at)}`,
                title: formatDateTime(status.last_synced_at),
              })
            : h("span", { text: "最近同步：尚未完成" }),
        ),
        status.last_error
          ? h("p", {
              class: "repo-error",
              text: status.last_error,
              title: status.last_error,
            })
          : null,
      ),
      h(
        "div",
        { class: "repo-branch-editor" },
        h(
          "label",
          { class: "stack-field repo-branch-field" },
          h("span", { text: "追踪分支" }),
          branch,
        ),
        h("span", {
          class: "repo-branch-note",
          text: repo.branch ? `指定分支：${repo.branch}` : "配置为空时跟随默认分支",
        }),
        h("div", { class: "repo-row-actions" }, save, remove),
      ),
    );
  });
  dom.repoManageList.replaceChildren(...rows);
  updateRepoManagementState(running);
}

function resetFilters() {
  app.filters = { query: "", state: "", kind: "", tag: "", repos: [], stale: false };
  dom.searchInput.value = "";
  renderFilterOptions();
  renderStats();
  renderTable();
}

function bindEvents() {
  let searchTimer = null;
  dom.searchInput.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      app.filters.query = dom.searchInput.value;
      renderStats();
      renderTable();
    }, 100);
  });
  dom.stateFilter.addEventListener("change", () => {
    app.filters.state = dom.stateFilter.value;
    renderStats();
    renderTable();
  });
  dom.kindFilter.addEventListener("change", () => {
    app.filters.kind = dom.kindFilter.value;
    renderStats();
    renderTable();
  });
  dom.tagFilter.addEventListener("change", () => {
    app.filters.tag = dom.tagFilter.value;
    renderStats();
    renderTable();
  });
  dom.staleFilter.addEventListener("change", () => {
    app.filters.stale = dom.staleFilter.checked;
    renderStats();
    renderTable();
  });
  dom.resetFilters.addEventListener("click", resetFilters);
  dom.syncButton.addEventListener("click", startSync);
  dom.repoFilterButton.addEventListener("click", () => {
    if (!dom.popover.hidden) {
      closePopover();
      return;
    }
    openRepoFilter(dom.repoFilterButton);
  });
  dom.manageRepos.addEventListener("click", openReposDialog);
  dom.manageTags.addEventListener("click", openTagsDialog);
  dom.manageFields.addEventListener("click", openFieldsDialog);

  dom.statsGrid.addEventListener("click", (event) => {
    const card = event.target.closest("[data-stat]");
    if (!card) return;
    const key = card.dataset.stat;
    if (key === "total") {
      resetFilters();
      return;
    }
    if (key === "open" || key === "closed") {
      app.filters.state = key;
      app.filters.kind = "";
    } else if (key === "issues" || key === "prs") {
      app.filters.kind = key === "issues" ? "issue" : "pr";
      app.filters.state = "";
    } else if (key === "stale") {
      app.filters.stale = true;
    }
    renderFilterOptions();
    renderStats();
    renderTable();
  });

  dom.tableHead.addEventListener("click", (event) => {
    const th = event.target.closest("[data-sort]");
    if (!th) return;
    const key = th.dataset.sort;
    if (app.sort.key === key) {
      app.sort.direction = app.sort.direction === "asc" ? "desc" : "asc";
    } else {
      app.sort.key = key;
      app.sort.direction = key === "updated_at" || key === "stale_days" ? "desc" : "asc";
    }
    renderTable();
  });

  dom.tableBody.addEventListener("click", (event) => {
    const action = event.target.closest("[data-action]");
    if (action) {
      const item = app.items.find(
        (candidate) => String(candidate.id) === action.dataset.itemId,
      );
      if (!item) return;
      if (action.dataset.action === "edit-tags") {
        openTagPicker(action, item);
      } else if (action.dataset.action === "edit-field") {
        const field = app.fields.find(
          (candidate) => String(candidate.id) === action.dataset.fieldId,
        );
        if (!field) return;
        if (field.type === "checkbox") toggleCheckboxField(item, field, action);
        else openFieldEditor(action, item, field);
      } else if (action.dataset.action === "sync") {
        startSync();
      } else if (action.dataset.action === "reset") {
        resetFilters();
      }
    }
  });

  for (const button of document.querySelectorAll("[data-close-dialog]")) {
    button.addEventListener("click", () => {
      const dialog = document.getElementById(button.dataset.closeDialog);
      if (dialog?.open) dialog.close();
    });
  }
  for (const dialog of [dom.tagsDialog, dom.fieldsDialog, dom.reposDialog]) {
    dialog.addEventListener("click", (event) => {
      if (event.target === dialog) dialog.close();
    });
  }

  dom.repoCreateForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const repo = dom.newRepoName.value.trim();
    const branch = dom.newRepoBranch.value.trim();
    if (!repo) return;
    const submit = dom.repoCreateForm.querySelector('button[type="submit"]');
    submit.disabled = true;
    submit.textContent = "正在验证…";
    try {
      await api("/api/repos", {
        method: "POST",
        body: { repo, branch },
      });
      dom.newRepoName.value = "";
      dom.newRepoBranch.value = "";
      await loadState({ silent: true });
      toast("仓库已添加", "success");
      dom.newRepoName.focus();
    } catch (error) {
      toast(error.message);
    } finally {
      submit.textContent = "添加仓库";
      updateRepoManagementState();
    }
  });

  dom.tagCreateForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = dom.newTagName.value.trim();
    if (!name) return;
    const submit = dom.tagCreateForm.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      await api("/api/tags", {
        method: "POST",
        body: { name, color: dom.newTagColor.value },
      });
      dom.newTagName.value = "";
      await loadState({ silent: true });
      toast("标签已创建", "success");
      dom.newTagName.focus();
    } catch (error) {
      toast(error.message);
    } finally {
      submit.disabled = false;
    }
  });

  dom.newFieldType.addEventListener("change", () => {
    const needsOptions =
      dom.newFieldType.value === "select" || dom.newFieldType.value === "multi_select";
    dom.fieldOptionsWrap.hidden = !needsOptions;
  });

  dom.fieldCreateForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = dom.newFieldName.value.trim();
    const type = dom.newFieldType.value;
    if (!name) return;
    const payload = { name, type };
    if (type === "select" || type === "multi_select") {
      payload.options = parseOptions(dom.newFieldOptions.value);
      if (!payload.options.length) {
        toast("单选或多选字段至少需要一个选项");
        return;
      }
    }
    const submit = dom.fieldCreateForm.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      await api("/api/fields", { method: "POST", body: payload });
      dom.newFieldName.value = "";
      dom.newFieldOptions.value = "";
      await loadState({ silent: true });
      toast("字段已创建", "success");
      dom.newFieldName.focus();
    } catch (error) {
      toast(error.message);
    } finally {
      submit.disabled = false;
    }
  });

  document.addEventListener("pointerdown", (event) => {
    if (dom.popover.hidden) return;
    if (dom.popover.contains(event.target)) return;
    if (dom.repoFilterButton.contains(event.target)) return;
    closePopover();
  });

  document.addEventListener(
    "scroll",
    (event) => {
      if (dom.popover.hidden) return;
      if (dom.popover.contains(event.target)) return;
      closePopover();
    },
    true,
  );

  window.addEventListener("resize", closePopover);

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !dom.popover.hidden) {
      closePopover();
    }
    const target = event.target;
    const editing =
      target instanceof HTMLElement &&
      (target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT" ||
        target.isContentEditable);
    if (
      event.key === "/" &&
      !editing &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey
    ) {
      event.preventDefault();
      dom.searchInput.focus();
    }
  });
}

function cacheDom() {
  dom.workspaceMeta = document.getElementById("workspaceMeta");
  dom.syncState = document.getElementById("syncState");
  dom.syncDot = document.getElementById("syncDot");
  dom.syncText = document.getElementById("syncText");
  dom.syncButton = document.getElementById("syncButton");
  dom.statsGrid = document.getElementById("statsGrid");
  dom.searchInput = document.getElementById("searchInput");
  dom.stateFilter = document.getElementById("stateFilter");
  dom.kindFilter = document.getElementById("kindFilter");
  dom.tagFilter = document.getElementById("tagFilter");
  dom.staleFilter = document.getElementById("staleFilter");
  dom.resetFilters = document.getElementById("resetFilters");
  dom.repoFilterButton = document.getElementById("repoFilterButton");
  dom.repoFilterLabel = document.getElementById("repoFilterLabel");
  dom.repoFilterCount = document.getElementById("repoFilterCount");
  dom.manageRepos = document.getElementById("manageRepos");
  dom.manageTags = document.getElementById("manageTags");
  dom.manageFields = document.getElementById("manageFields");
  dom.tableScroller = document.getElementById("tableScroller");
  dom.tableHead = document.getElementById("tableHead");
  dom.tableBody = document.getElementById("tableBody");
  dom.resultCount = document.getElementById("resultCount");
  dom.lastSyncDetail = document.getElementById("lastSyncDetail");
  dom.popover = document.getElementById("popover");
  dom.toast = document.getElementById("toast");
  dom.tagsDialog = document.getElementById("tagsDialog");
  dom.fieldsDialog = document.getElementById("fieldsDialog");
  dom.reposDialog = document.getElementById("reposDialog");
  dom.tagCreateForm = document.getElementById("tagCreateForm");
  dom.newTagName = document.getElementById("newTagName");
  dom.newTagColor = document.getElementById("newTagColor");
  dom.tagManageList = document.getElementById("tagManageList");
  dom.fieldCreateForm = document.getElementById("fieldCreateForm");
  dom.newFieldName = document.getElementById("newFieldName");
  dom.newFieldType = document.getElementById("newFieldType");
  dom.newFieldOptions = document.getElementById("newFieldOptions");
  dom.fieldOptionsWrap = document.getElementById("fieldOptionsWrap");
  dom.fieldManageList = document.getElementById("fieldManageList");
  dom.repoCreateForm = document.getElementById("repoCreateForm");
  dom.newRepoName = document.getElementById("newRepoName");
  dom.newRepoBranch = document.getElementById("newRepoBranch");
  dom.repoManageList = document.getElementById("repoManageList");
}

async function init() {
  cacheDom();
  bindEvents();
  await loadState();
}

init();
