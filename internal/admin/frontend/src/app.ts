import type {
    SidebarData,
    DashboardData,
    ProjectSummary,
    ProjectData,
    ReleaseData,
    RegistriesData,
    TokenInfo,
    SitesData,
    OIDCPolicy,
    AllArtifact,
    StorageData,
    NavItem,
    ProjectGroupInfo,
    ServiceURLs,
    RetentionData,
} from "./types";
import { Html } from "./html";

let demo = false;
let sidebarCache: SidebarData | null = null;

function h(s: string | number | null | undefined): string {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

function humanSize(b: number): string {
    if (b < 1024) return b + " B";
    const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
    let i = -1;
    let v = b;
    do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
    return v.toFixed(1) + " " + units[i];
}

function timeAgo(s: string): string {
    if (!s) return "-";
    const d = Date.now() - new Date(s).getTime();
    if (d < 60000) return "just now";
    const m = Math.floor(d / 60000);
    if (m < 60) return m === 1 ? "1 minute ago" : m + " minutes ago";
    const hr = Math.floor(m / 60);
    if (hr < 24) return hr === 1 ? "1 hour ago" : hr + " hours ago";
    const days = Math.floor(hr / 24);
    return days === 1 ? "1 day ago" : days + " days ago";
}

function formatTime(s: string): string {
    if (!s) return "-";
    const d = new Date(s);
    if (isNaN(d.getTime())) return "-";
    const pad = (n: number): string => n < 10 ? "0" + n : "" + n;
    return d.getUTCFullYear() + "-" + pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate()) +
        " " + pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()) + " UTC";
}

// A path with no demo entry falls back to {} rather than undefined: pages read
// `d.foo || []` off the result, so undefined throws before rendering anything.
function apiFetch<T>(path: string): Promise<T> {
    if (demo) return Promise.resolve((demoData[path] || {}) as T);
    return fetch("/api" + path).then((r) => {
        if (!r.ok) throw new Error(String(r.status));
        return r.json() as Promise<T>;
    }).catch(() => {
        demo = true;
        return (demoData[path] || {}) as T;
    });
}

function setTitle(t: string): void {
    document.title = t + " - Buildhost Admin";
}

const NAV_ITEMS: NavItem[] = [
    { id: "dashboard", href: "#/", label: "Dashboard", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M10.707 2.293a1 1 0 00-1.414 0l-7 7a1 1 0 001.414 1.414L4 10.414V17a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 011-1h2a1 1 0 011 1v2a1 1 0 001 1h2a1 1 0 001-1v-6.586l.293.293a1 1 0 001.414-1.414l-7-7z"/></svg>' },
    { id: "projects", href: "#/projects", label: "Projects", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>' },
    { id: "registries", href: "#/registries", label: "Registries", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4 4a2 2 0 012-2h8a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V4zm2 0h8v3H6V4zm0 5h8v2H6V9zm0 4h5v2H6v-2z" clip-rule="evenodd"/></svg>' },
    { id: "tokens", href: "#/tokens", label: "Tokens", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M18 8a6 6 0 01-7.743 5.743L10 14l-1 1-1 1H6v2H2v-4l4.257-4.257A6 6 0 1118 8zm-6-4a1 1 0 100 2 2 2 0 012 2 1 1 0 102 0 4 4 0 00-4-4z" clip-rule="evenodd"/></svg>' },
    { id: "sites", href: "#/sites", label: "Sites", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4.083 9h1.946c.089-1.546.383-2.97.837-4.118A6.004 6.004 0 004.083 9zM10 2a8 8 0 100 16 8 8 0 000-16zm0 2c-.076 0-.232.032-.465.262-.238.234-.497.623-.737 1.182-.389.907-.673 2.142-.766 3.556h3.936c-.093-1.414-.377-2.649-.766-3.556-.24-.56-.5-.948-.737-1.182C10.232 4.032 10.076 4 10 4zm3.971 5c-.089-1.546-.383-2.97-.837-4.118A6.004 6.004 0 0115.917 9h-1.946zm-2.003 2H8.032c.093 1.414.377 2.649.766 3.556.24.56.5.948.737 1.182.233.23.389.262.465.262.076 0 .232-.032.465-.262.238-.234.497-.623.737-1.182.389-.907.673-2.142.766-3.556zm1.166 4.118c.454-1.147.748-2.572.837-4.118h1.946a6.004 6.004 0 01-2.783 4.118zm-6.268 0C6.412 13.97 6.118 12.546 6.029 11H4.083a6.004 6.004 0 002.783 4.118z" clip-rule="evenodd"/></svg>' },
    { id: "oidc", href: "#/oidc", label: "OIDC Policies", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>' },
    { id: "retention", href: "#/retention", label: "Retention", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>' },
];

function renderSidebar(nav: string): void {
    const build = sidebarCache?.build;
    let links = "";
    for (const n of NAV_ITEMS) {
        links += '<li><a href="' + n.href + '"' + (n.id === nav ? ' class="active"' : '') + '>' + n.icon + " " + h(n.label) + "</a></li>";
    }
    let footer = "";
    if (build?.commit_url) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <a href="' + h(build.commit_url) + '" class="sidebar-info-link">' + h(build.short_commit) + "</a></div>";
    } else if (build?.commit) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <span>' + h(build.short_commit) + "</span></div>";
    }
    if (sidebarCache?.build_age) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Built</span> <span>' + h(sidebarCache.build_age) + "</span></div>";
    if (sidebarCache?.cpu_percent) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">CPU</span> <span>' + h(sidebarCache.cpu_percent) + "</span></div>";
    if (sidebarCache?.disk_total) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Disk</span> <span>' + h(sidebarCache.disk_used) + " / " + h(sidebarCache.disk_total) + "</span></div>";

    document.getElementById("sidebar")!.innerHTML =
        '<div class="sidebar-header"><div class="logo">B</div><div><div class="sidebar-title">Buildhost</div><div class="sidebar-subtitle">Admin Dashboard</div></div></div>' +
        '<ul class="nav-list">' + links + "</ul>" +
        '<div class="sidebar-footer">' + footer + "</div>";
}

function badge(type: string, text: string): string {
    return '<span class="badge badge-' + type + '">' + h(text) + "</span>";
}

// urlTpl renders a copyable URL with inline os/arch dropdowns. `base` is the text
// before the os dropdown, `mid` the text BETWEEN the os and arch dropdowns (e.g.
// "&arch=" for the query-param download URLs -- it is not always "/"), and
// `suffix` optional text after. `tpl` is the full template with {os}/{arch}
// placeholders that the copy button substitutes the selected values into.
function urlTpl(tpl: string, base: string, mid: string, suffix?: string): string {
    return '<span class="url-tpl" data-tpl="' + h(tpl) + '">' +
        "<code>" + h(base) + "</code>" +
        '<select class="tpl-select" data-var="os"><option value="linux">linux</option><option value="darwin">darwin</option><option value="windows">windows</option><option value="freebsd">freebsd</option></select>' +
        "<code>" + h(mid) + "</code>" +
        '<select class="tpl-select" data-var="arch"><option value="amd64">amd64</option><option value="arm64">arm64</option><option value="386">386</option><option value="arm">arm</option></select>' +
        (suffix ? "<code>" + h(suffix) + "</code>" : "") +
        "</span><copy-btn></copy-btn>";
}

function codeBlock(label: string, code: string): string {
    return '<div class="code-block"><div class="code-label">' + h(label) +
        '<copy-btn class="code-copy-btn" data-src="pre"></copy-btn></div><pre>' + h(code) + "</pre></div>";
}

// --- Pages ---

function pageDashboard(): void {
    setTitle("Dashboard");
    renderSidebar("dashboard");
    apiFetch<DashboardData>("/dashboard").then((d) => {
        const s = d.stats || {} as DashboardData["stats"];
        const b = d.build || {} as DashboardData["build"];
        const cfg = d.config || {} as DashboardData["config"];
        let html = '<h1>Dashboard</h1><div class="stat-grid">';
        const cards: [string | number, string, string][] = [
            [s.project_count, "Projects", "#/projects"],
            [s.release_count, "Releases", "#/projects"],
            [s.artifact_count, "Artifacts", "#/artifacts"],
            [humanSize(s.total_storage_bytes || 0), "Storage Used", "#/storage"],
            [s.token_count, "API Tokens", "#/tokens"],
            [s.site_count || 0, "Sites", "#/sites"],
        ];
        for (const card of cards) html += '<a href="' + card[2] + '" class="stat-card stat-card-link"><div class="stat-value">' + h(card[0]) + '</div><div class="stat-label">' + card[1] + "</div></a>";
        html += "</div>";

        html += '<div class="card"><h2>Server Status</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Version</td><td>" + h(b.version) + "</td></tr>";
        if (b.commit_url) html += "<tr><td class='info-label'>Commit</td><td><a href='" + h(b.commit_url) + "'><code class='commit'>" + h(b.short_commit) + "</code></a></td></tr>";
        else html += "<tr><td class='info-label'>Commit</td><td><code>" + h(b.commit) + "</code></td></tr>";
        html += "<tr><td class='info-label'>Built</td><td>" + h(b.date || "-") + "</td></tr>";
        html += "<tr><td class='info-label'>Uptime</td><td>" + h(d.uptime) + "</td></tr>";
        html += "<tr><td class='info-label'>CPU Usage</td><td>" + h(d.cpu_percent) + "</td></tr>";
        html += "<tr><td class='info-label'>CPU Time</td><td>" + h(d.cpu_total) + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Configuration</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Base URL</td><td>" + h(cfg.base_url) + "</td></tr>";
        html += "<tr><td class='info-label'>API Listen</td><td>" + h(cfg.listen_addr) + "</td></tr>";
        html += "<tr><td class='info-label'>Admin Listen</td><td>" + h(cfg.admin_listen_addr) + "</td></tr>";
        html += "<tr><td class='info-label'>Data Directory</td><td>" + h(cfg.data_dir) + "</td></tr>";
        const issuers = (cfg.oidc_issuers || []).map((v: string) => "<code>" + h(v) + "</code>").join(", ");
        html += "<tr><td class='info-label'>Trusted OIDC Issuers</td><td>" + (issuers || '<span class="empty">None</span>') + "</td></tr>";
        const orgs = (cfg.oidc_orgs || []).map((v: string) => "<code>" + h(v) + "</code>").join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Orgs</td><td>" + (orgs || '<span class="empty">None</span>') + "</td></tr>";
        const events = (cfg.oidc_events || []).map((v: string) => "<code>" + h(v) + "</code>").join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Events</td><td>" + events + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Recent Releases</h2><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Branch</th><th>Status</th><th>Created</th></tr></thead><tbody>';
        const recent = d.recent || [];
        if (recent.length === 0) {
            html += '<tr><td colspan="5" class="empty">No releases yet</td></tr>';
        } else {
            for (const rel of recent) {
                html += "<tr><td><a href='#/projects/" + h(rel.project_name) + "'>" + h(rel.project_name) + "</a></td>";
                html += "<td><a href='#/projects/" + h(rel.project_name) + "/releases/" + h(rel.version) + "'><code>" + h(rel.version) + "</code></a></td>";
                html += "<td>" + (rel.git_branch ? "<code>" + h(rel.git_branch) + "</code>" : "-") + "</td>";
                html += "<td>" + (rel.published ? badge("success", "Published") : badge("warning", "Draft")) + "</td>";
                html += '<td title="' + h(formatTime(rel.created_at)) + '">' + h(timeAgo(rel.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
}

// Project names are slash-namespaced, so the list renders as a tree: each shared
// prefix becomes a folder row and the leaf shows only its own segment.
type TreeNode = { name: string; children: Record<string, TreeNode>; project: ProjectSummary | null };
type TreeRow = { kind: "folder"; depth: number; name: string } | { kind: "project"; depth: number; project: ProjectSummary };

function projectTreeRows(projects: ProjectSummary[]): TreeRow[] {
    const root: TreeNode = { name: "", children: {}, project: null };
    for (const p of projects) {
        let cur = root;
        for (const part of String(p.name || "").split("/")) {
            if (!cur.children[part]) cur.children[part] = { name: part, children: {}, project: null };
            cur = cur.children[part];
        }
        cur.project = p;
    }

    const out: TreeRow[] = [];
    const walk = (node: TreeNode, depth: number): void => {
        for (const name of Object.keys(node.children).sort()) {
            const child = node.children[name];
            if (Object.keys(child.children).length > 0) out.push({ kind: "folder", depth, name: child.name });
            if (child.project) out.push({ kind: "project", depth, project: child.project });
            walk(child, depth + 1);
        }
    };
    walk(root, 0);
    return out;
}

function projectLabel(name: string): string {
    const s = String(name || "");
    const i = s.lastIndexOf("/");
    return i >= 0 ? s.substring(i + 1) : s;
}

function projectNameCell(project: ProjectSummary, depth: number): string {
    const name = project.name || "";
    const label = projectLabel(name);
    let cell = '<span class="project-label"><a href="#/projects/' + h(name) + '">' + h(label) + "</a></span>";
    if (label !== name) cell += '<span class="project-path">' + h(name) + "</span>";
    return '<td class="project-name-cell project-depth-' + depth + '">' + cell + "</td>";
}

function pageProjects(): void {
    setTitle("Projects");
    renderSidebar("projects");
    apiFetch<ProjectSummary[]>("/projects").then((projects) => {
        let html = '<h1>Projects</h1><div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Description</th><th>Versioning</th><th>Visibility</th><th>Releases</th><th>Artifacts</th><th>Created</th></tr></thead><tbody>';
        if (projects.length === 0) {
            html += '<tr><td colspan="7" class="empty">No projects yet</td></tr>';
        } else {
            for (const row of projectTreeRows(projects)) {
                if (row.kind === "folder") {
                    html += '<tr class="project-folder-row"><td colspan="7" class="project-folder-cell project-depth-' + row.depth + '"><span class="project-folder-icon"></span>' + h(row.name) + "</td></tr>";
                    continue;
                }
                const p = row.project;
                html += "<tr class='project-tree-row'>";
                html += projectNameCell(p, row.depth);
                html += '<td class="truncate">' + h(p.description) + "</td>";
                html += "<td>" + badge("neutral", p.versioning) + "</td>";
                html += "<td>" + (p.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
                html += "<td>" + p.release_count + "</td><td>" + p.artifact_count + "</td>";
                html += '<td title="' + h(formatTime(p.created_at)) + '">' + h(timeAgo(p.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
}

function pageProject(name: string): void {
    setTitle(name);
    renderSidebar("projects");
    apiFetch<ProjectData>("/projects/" + encodeURIComponent(name)).then((d) => {
        const p = d.project;
        let html = "<h1>" + h(p.name) + "</h1>";
        html += '<div class="card"><h2>Project Info</h2><table class="info-table">';
        html += "<tr><td class='info-label'>ID</td><td>" + p.id + "</td></tr>";
        if (p.description) html += "<tr><td class='info-label'>Description</td><td>" + h(p.description) + "</td></tr>";
        if (p.homepage) html += "<tr><td class='info-label'>Homepage</td><td>" + h(p.homepage) + "</td></tr>";
        if (p.license) html += "<tr><td class='info-label'>License</td><td>" + h(p.license) + "</td></tr>";
        html += "<tr><td class='info-label'>Versioning</td><td>" + badge("neutral", p.versioning) + "</td></tr>";
        html += "<tr><td class='info-label'>Visibility</td><td>" + (p.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td></tr>";
        html += '<tr><td class="info-label">Created</td><td title="' + h(formatTime(p.created_at)) + '">' + h(timeAgo(p.created_at)) + "</td></tr>";
        html += '<tr><td class="info-label">Updated</td><td title="' + h(formatTime(p.updated_at)) + '">' + h(timeAgo(p.updated_at)) + "</td></tr>";
        html += "</table></div>";

        const rels = d.releases || [];

        // Download & Install (latest): non-versioned endpoints and commands so the
        // newest build can be fetched without first opening a specific release.
        // Only shown once the project has a published release ("latest" 404s
        // otherwise).
        const svc = (d.services || {}) as Partial<ServiceURLs>;
        let hasPublished = false;
        for (const r of rels) { if (r.published) { hasPublished = true; break; } }
        if (hasPublished) {
            const dlBase = (svc.dl || "") + "/" + p.name;
            const aptBase = (svc.apt || "") + "/" + p.name;
            const brewU = (svc.brew || "") + "/Formula/" + p.name + ".rb";
            const npmU = (svc.npm || "") + "/@buildhost/" + p.name;
            const ociU = (svc.oci || "") + "/v2/" + p.name + "/manifests/latest";
            const npmHost = (svc.npm || "").replace(/^https?:\/\//, "");
            const ociHost = (svc.oci || "").replace(/^https?:\/\//, "");
            const aptHost = (svc.apt || "").replace(/^https?:\/\//, "");
            const staticHost = (svc.static || "").replace(/^https?:\/\//, "");
            // Slash-namespaced names keep their slash in the repo URL but install
            // under a folded Debian package name (repackage.DebPackageName folds
            // '/' and '_' to '-'), so apt and dpkg agree.
            const aptPkg = p.name.replace(/[/_]/g, "-");
            const priv = !!p.is_private;

            // endpointRow: an <a> + copy-button cell for a fixed (latest) service
            // endpoint. dataCopy, when set, is what the copy button yields (APT
            // links to the Release file but copies the repo base).
            const endpointRow = (label: string, text: string, href: string, dataCopy: string | null) => {
                const link = Html.a(text).attr("href", href);
                if (dataCopy) link.attr("data-copy", dataCopy);
                return Html.tr(
                    Html.td(label).cls("info-label"),
                    Html.td(link, Html.el("copy-btn").attr("data-src", "a")).cls("endpoint-cell"),
                );
            };

            // curl direct download. A private project's dl link redirects to the
            // static host, and curl only re-sends credentials across that hop with
            // --location-trusted; the token is the HTTP Basic password.
            const curlCmd = priv
                ? 'curl -fsSL --location-trusted -u "token:$TOKEN" -O \\\n  "' + dlBase + '?os=linux&arch=amd64"'
                : 'curl -fsSL -O "' + dlBase + '?os=linux&arch=amd64"';

            // APT: the server-generated install.sh is the one-line path (it saves
            // the armored key, writes the signed-by source, records the token for
            // private repos, and refreshes the index).
            const aptOneLiner = priv
                ? 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + aptBase + "/install.sh \\\n  | sudo BUILDHOST_TOKEN=$TOKEN sh"
                : "curl -fsSL " + aptBase + "/install.sh | sudo sh";

            const aptCmd = priv
                ? 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + aptPkg + '.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + aptHost + ' login token password $TOKEN\nmachine ' + staticHost + ' login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install ' + aptPkg
                : 'sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + aptPkg + '.list\nsudo apt update && sudo apt install ' + aptPkg;

            const npmCmd = priv
                ? "npm config set //" + npmHost + "/:_authToken $TOKEN\nnpm install @buildhost/" + p.name + " --registry " + (svc.npm || "")
                : "npm install @buildhost/" + p.name + " --registry " + (svc.npm || "");

            const dockerCmd = priv
                ? "echo $TOKEN | docker login " + ociHost + " -u token --password-stdin\ndocker pull " + ociHost + "/" + p.name + ":latest"
                : "docker pull " + ociHost + "/" + p.name + ":latest";

            html += Html.div(
                Html.h2("Download & Install ", Html.span("— latest").cls("muted").style("font-weight:400")),
                Html.p("Always resolves to the newest published release. To pin a specific version, open a release below.").cls("section-desc"),
                Html.table(
                    Html.tr(
                        Html.td("Direct download").cls("info-label"),
                        Html.td(Html.raw(urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch="))).cls("endpoint-cell"),
                    ),
                    endpointRow("APT", aptBase, aptBase + "/dists/stable/Release", aptBase),
                    endpointRow("APT installer", aptBase + "/install.sh", aptBase + "/install.sh", null),
                    endpointRow("Homebrew", brewU, brewU, null),
                    endpointRow("npm", npmU, npmU, null),
                    endpointRow("OCI", ociU, ociU, null),
                ).cls("info-table"),
                Html.raw(codeBlock("Direct download (curl)", curlCmd)),
                Html.raw(codeBlock("APT (one-line install)", aptOneLiner)),
                Html.raw(codeBlock("APT (manual setup)", aptCmd)),
                Html.raw(codeBlock("Homebrew", "brew tap pazer/build " + (svc.brew || "") + "/tap.git\nbrew trust pazer/build\nbrew install pazer/build/" + p.name)),
                Html.raw(codeBlock("npm", npmCmd)),
                Html.raw(codeBlock("Docker", dockerCmd)),
            ).cls("card").toString();
        }

        html += '<div class="card"><h2>Releases</h2><table class="data-table"><thead><tr><th>Version</th><th>Branch</th><th>Commit</th><th>Status</th><th>Artifacts</th><th>Published</th><th>Created</th></tr></thead><tbody>';
        if (rels.length === 0) {
            html += '<tr><td colspan="7" class="empty">No releases yet</td></tr>';
        } else {
            for (const r of rels) {
                html += "<tr><td><a href='#/projects/" + h(p.name) + "/releases/" + h(r.version) + "'><code>" + h(r.version) + "</code></a></td>";
                html += "<td>" + (r.git_branch ? "<code>" + h(r.git_branch) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.git_commit ? '<code class="commit">' + h(r.git_commit) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.published ? badge("success", "Published") : badge("warning", "Draft")) + "</td>";
                html += "<td>" + r.artifact_count + "</td>";
                html += "<td>" + h(formatTime(r.published_at)) + "</td>";
                html += '<td title="' + h(formatTime(r.created_at)) + '">' + h(timeAgo(r.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        const sites = d.sites || [];
        if (sites.length > 0) {
            const bu = d.base_url || "";
            html += '<div class="card"><h2>Sites</h2><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
            for (const si of sites) {
                html += "<tr><td><code>" + h(si.branch) + "</code></td>";
                html += "<td>" + si.file_count + "</td>";
                html += "<td>" + h(humanSize(si.size)) + "</td>";
                html += "<td>" + (si.git_commit ? '<code class="commit">' + h(si.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + h(formatTime(si.updated_at)) + '">' + h(timeAgo(si.updated_at)) + "</td>";
                html += '<td><a href="' + h((svc.sites || "") + "/" + p.name + "/branch/" + si.branch + "/") + '" target="_blank">Open</a></td></tr>';
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content")!.innerHTML = html;
    });
}

function pageRelease(name: string, version: string): void {
    setTitle(name + " " + version);
    renderSidebar("projects");
    apiFetch<ReleaseData>("/projects/" + encodeURIComponent(name) + "/releases/" + encodeURIComponent(version)).then((d) => {
        const p = d.project, r = d.release, bu = d.base_url;
        const svc = (d.services || {}) as Partial<ServiceURLs>;
        const dlBase = (svc.dl || "") + "/" + p.name;
        const priv = !!p.is_private;
        let html = "<h1><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a> / " + h(r.version) + "</h1>";

        html += '<div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + (d.artifacts || []).length + '</div><div class="stat-label">Artifacts</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.total_size || 0)) + '</div><div class="stat-label">Total Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + (d.total_downloads || 0) + '</div><div class="stat-label">Downloads</div></div>';
        html += "</div>";

        html += '<div class="card"><h2>Release Info</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Version</td><td><code>" + h(r.version) + "</code></td></tr>";
        html += "<tr><td class='info-label'>Status</td><td>" + (r.published ? badge("success", "Published") : badge("warning", "Draft")) + "</td></tr>";
        if (r.git_branch) html += "<tr><td class='info-label'>Branch</td><td><code>" + h(r.git_branch) + "</code></td></tr>";
        if (r.git_commit) html += "<tr><td class='info-label'>Commit</td><td><code>" + h(r.git_commit) + "</code></td></tr>";
        if (r.notes) html += "<tr><td class='info-label'>Notes</td><td>" + h(r.notes) + "</td></tr>";
        html += "<tr><td class='info-label'>Published At</td><td>" + h(formatTime(r.published_at)) + "</td></tr>";
        html += "<tr><td class='info-label'>Created At</td><td>" + h(formatTime(r.created_at)) + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Artifacts</h2><table class="data-table"><thead><tr><th>Platform</th><th>Kind</th><th>Filename</th><th>Size</th><th>Downloads</th><th>Links</th></tr></thead><tbody>';
        const arts = d.artifacts || [];
        if (arts.length === 0) {
            html += '<tr><td colspan="6" class="empty">No artifacts</td></tr>';
        } else {
            for (const a of arts) {
                html += "<tr><td>" + badge("info", a.os + "/" + a.arch) + "</td>";
                html += "<td>" + badge("neutral", a.kind) + "</td>";
                html += "<td>" + (a.filename ? "<code>" + h(a.filename) + "</code>" : '<span class="muted">-</span>') + "</td>";
                html += "<td>" + h(humanSize(a.size)) + "</td>";
                html += "<td>" + a.download_count + "</td>";
                html += '<td class="dl-links">';
                const pkgs = a.packages || [];
                if (priv) {
                    // Private project: a plain dl link would 401. Each link mints a
                    // signed, single-artifact link on click, then downloads it.
                    html += dlMintLink(p.name, r.version, a.os, a.arch, "raw", false, "raw", "Download (mints a temporary signed link)");
                    if (a.debug_storage_key) html += " " + dlMintLink(p.name, r.version, a.os, a.arch, "raw", true, "debug", "Debug symbols");
                    for (const pkg of pkgs) {
                        html += " " + dlMintLink(p.name, r.version, a.os, a.arch, pkg.format, false, pkg.format, pkg.filename + " (" + humanSize(pkg.size) + ")");
                    }
                    html += ' <button type="button" class="dl-share" onclick="App.copyTempLink(this,\'' + h(p.name) + "','" + h(r.version) + "','" + h(a.os) + "','" + h(a.arch) + '\',\'raw\')" title="Copy a temporary 1-hour shareable link">temp link</button>';
                } else {
                    const dlQ = "?v=" + r.version + "&os=" + a.os + "&arch=" + a.arch;
                    html += '<a href="' + h(dlBase + dlQ) + '" title="Direct download">raw</a>';
                    if (a.debug_storage_key) html += ' <a href="' + h(dlBase + dlQ + "&debug=1") + '" title="Debug symbols">debug</a>';
                    for (const pkg of pkgs) {
                        html += ' <a href="' + h(dlBase + dlQ + "&fmt=" + pkg.format) + '" title="' + h(pkg.filename) + " (" + h(humanSize(pkg.size)) + ')">' + h(pkg.format) + "</a>";
                    }
                }
                html += "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        // Every endpoint lives on its own service subdomain. base_url is the apex
        // and serves none of these paths, so these must come from `services`.
        const aptU = (svc.apt || "") + "/" + p.name;
        const brewU = (svc.brew || "") + "/Formula/" + p.name + ".rb";
        const npmU = (svc.npm || "") + "/@buildhost/" + p.name;
        const ociU = (svc.oci || "") + "/v2/" + p.name + "/manifests/" + r.version;

        html += '<div class="card"><h2>Download Endpoints</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Direct (latest)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>Direct (version)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?v=" + r.version + "&os={os}&arch={arch}", dlBase + "?v=" + r.version + "&os=", "&arch=") + "</td></tr>";
        if (r.git_branch) html += "<tr><td class='info-label'>Direct (branch)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?branch=" + r.git_branch + "&os={os}&arch={arch}", dlBase + "?branch=" + r.git_branch + "&os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>APT</td><td class='endpoint-cell'><a href='" + h(aptU + "/dists/stable/Release") + "' data-copy='" + h(aptU) + "'>" + h(aptU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Homebrew</td><td class='endpoint-cell'><a href='" + h(brewU) + "'>" + h(brewU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>npm</td><td class='endpoint-cell'><a href='" + h(npmU) + "'>" + h(npmU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>OCI</td><td class='endpoint-cell'><a href='" + h(ociU) + "'>" + h(ociU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "</table></div>";

        document.getElementById("content")!.innerHTML = html;
    });
}

function pageRegistries(): void {
    setTitle("Registries");
    renderSidebar("registries");
    apiFetch<RegistriesData>("/registries").then((d) => {
        const bu = d.base_url;
        const svc = (d.services || {}) as Partial<ServiceURLs>;
        const dl = svc.dl || "", apt = svc.apt || "", brew = svc.brew || "", npm = svc.npm || "";
        const oci = svc.oci || "", sites = svc.sites || "", staticUrl = svc.static || "";
        const npmHost = npm.replace(/^https?:\/\//, ""), ociHost = oci.replace(/^https?:\/\//, "");
        const aptHost = apt.replace(/^https?:\/\//, ""), staticHost = staticUrl.replace(/^https?:\/\//, "");
        let html = "<h1>Registry Endpoints</h1>";

        html += '<div class="card"><h2>Direct Downloads</h2><p class="section-desc">Download artifacts directly by platform. OS and architecture are query parameters; version, latest, and branch resolution too.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Latest</td><td class='endpoint-cell'>" + urlTpl(dl + "/{project}?os={os}&arch={arch}", dl + "/{project}?os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>Version</td><td class='endpoint-cell'><code>" + h(dl + "/{project}?v={version}&os={os}&arch={arch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Branch</td><td class='endpoint-cell'><code>" + h(dl + "/{project}?branch={branch}&os={os}&arch={arch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("curl", 'curl -fsSL -H "Authorization: Bearer $TOKEN" \\\n  "' + dl + '/{project}?os=linux&arch=amd64" -o {project}');
        html += codeBlock("Query parameters", "?os=linux         # Required: target OS\n?arch=amd64       # Required: target architecture\n?v={version}      # Pin to a version (default: latest)\n?branch={branch}  # Latest build on a git branch\n?fmt=tar.gz       # Repackage: raw, tar.gz, tar.xz, tar.zst, zip\n?debug=1          # Debug symbols");
        html += "</div>";

        html += '<div class="card"><h2>APT Repository</h2><p class="section-desc">Debian/Ubuntu package repository. Packages are generated on demand at download time. The <code>install.sh</code> one-liner installs the signing key and source for you. The repository URL keeps a slash-namespaced project name, but the Debian package name folds <code>/</code> and <code>_</code> to <code>-</code> &mdash; e.g. <code>myrepo/server</code> installs as <code>myrepo-server</code>.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Release</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/dists/stable/Release") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>InRelease</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/dists/stable/InRelease") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Packages</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/dists/stable/main/binary-{arch}/Packages") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Pool</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/pool/{filename}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Signing key</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/key.asc") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Installer</td><td class='endpoint-cell'><code>" + h(apt + "/{project}/install.sh") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("One-line install (public project)", "curl -fsSL " + apt + "/{project}/install.sh | sudo sh");
        html += codeBlock("One-line install (private project)", 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + apt + "/{project}/install.sh \\\n  | sudo BUILDHOST_TOKEN=$TOKEN sh");
        html += codeBlock("Setup (public project)", "sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL " + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\nsudo apt update && sudo apt install {project}');
        html += codeBlock("Setup (private project)", 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + aptHost + " login token password $TOKEN\nmachine " + staticHost + " login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install {project}");
        html += "</div>";

        // The tap, not a bare formula URL: a formula alone has no tap to resolve
        // `brew trust` against, and a bare host 404s -- clone /tap.git.
        html += '<div class="card"><h2>Homebrew Tap</h2><p class="section-desc">Homebrew formulas are served through a generated Git tap. Formula files auto-detect macOS and Linux artifacts.</p>';
        html += '<table class="info-table"><tr><td class="info-label">Tap Git URL</td><td class="endpoint-cell"><code>' + h(brew + "/tap.git") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += '<tr><td class="info-label">Formula</td><td class="endpoint-cell"><code>' + h(brew + "/Formula/{project}.rb") + "</code><copy-btn data-src='code'></copy-btn></td></tr></table>";
        html += codeBlock("Install", "brew tap pazer/build " + brew + "/tap.git\nbrew trust pazer/build\nbrew install pazer/build/{project}");
        html += "</div>";

        html += '<div class="card"><h2>npm Registry</h2><p class="section-desc">npm-compatible registry. Packages are scoped under <code>@buildhost</code>.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Package metadata</td><td class='endpoint-cell'><code>" + h(npm + "/@buildhost/{project}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Tarball</td><td class='endpoint-cell'><code>" + h(npm + "/@buildhost/{project}/-/{project}-{version}.tgz") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("Setup", "npm config set @buildhost:registry " + npm + "/\nnpm config set //" + npmHost + "/:_authToken $TOKEN   # if private\nnpm install @buildhost/{project}");
        html += "</div>";

        html += '<div class="card"><h2>OCI Distribution (Docker)</h2><p class="section-desc">OCI-compatible registry for pulling artifacts as container images.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>API check</td><td class='endpoint-cell'><a href='" + h(oci + "/v2/") + "' data-copy='" + h(oci + "/v2/") + "'>" + h(oci + "/v2/") + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Manifest</td><td class='endpoint-cell'><code>" + h(oci + "/v2/{project}/manifests/{reference}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Blob</td><td class='endpoint-cell'><code>" + h(oci + "/v2/{project}/blobs/{digest}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("Docker pull", "docker pull " + ociHost + "/{project}:{version}");
        html += codeBlock("Private project", "echo $TOKEN | docker login " + ociHost + " -u token --password-stdin\ndocker pull " + ociHost + "/{project}:{version}");
        html += "</div>";

        html += '<div class="card"><h2>Static Sites</h2><p class="section-desc">Host small, self-contained static sites with independent per-branch deployments.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Deploy</td><td class='endpoint-cell'><code>PUT " + h(sites + "/{project}/branch/{branch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Serve</td><td class='endpoint-cell'><code>" + h(sites + "/{project}/branch/{branch}/{path}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Delete</td><td class='endpoint-cell'><code>DELETE " + h(sites + "/{project}/branch/{branch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>List branches</td><td class='endpoint-cell'><code>GET " + h(sites + "/{project}/branches") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("Deploy (curl)", 'tar czf - -C ./dist . | curl -X PUT \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/gzip" \\\n  --data-binary @- \\\n  ' + sites + "/{project}/branch/{branch}");
        html += "</div>";

        html += '<div class="card"><h2>REST API</h2><p class="section-desc">JSON API for managing projects, releases, and artifacts programmatically. Served on the main domain.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>List projects</td><td class='endpoint-cell'><a href='" + h(bu + "/api/v1/projects") + "'>GET " + h(bu + "/api/v1/projects") + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Get project</td><td class='endpoint-cell'><code>GET " + h(bu + "/api/v1/projects/{project}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>List releases</td><td class='endpoint-cell'><code>GET " + h(bu + "/api/v1/projects/{project}/releases") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Publish</td><td class='endpoint-cell'><code>POST " + h(bu + "/api/v1/projects/{project}/releases/{version}/publish") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += codeBlock("Authentication", "# Bearer token\ncurl -H \"Authorization: Bearer $TOKEN\" " + bu + "/api/v1/projects\n\n# Basic auth\ncurl -u \"token:$TOKEN\" " + bu + "/api/v1/projects\n\n# Query parameter (for clients that can't set headers)\ncurl \"" + bu + '/api/v1/projects?token=$TOKEN"');
        html += "</div>";

        const projects = d.projects || [];
        if (projects.length > 0) {
            html += '<div class="card"><h2>Projects</h2><p class="section-desc">Quick links to project-specific endpoints.</p>';
            html += '<table class="data-table"><thead><tr><th>Project</th><th>Visibility</th><th>Direct Download</th><th>APT</th><th>Brew</th><th>npm</th></tr></thead><tbody>';
            for (const pr of projects) {
                html += "<tr><td><a href='#/projects/" + h(pr.name) + "'>" + h(pr.name) + "</a></td>";
                html += "<td>" + (pr.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
                const prDl = dl + "/" + pr.name;
                html += "<td class='endpoint-cell'><span class='url-tpl' data-tpl='" + h(prDl + "?os={os}&arch={arch}") + "'><code class='truncate'>" + h(prDl + "?os=") + "</code><select class='tpl-select tpl-select-sm' data-var='os'><option value='linux'>linux</option><option value='darwin'>darwin</option><option value='windows'>windows</option><option value='freebsd'>freebsd</option></select><code>&arch=</code><select class='tpl-select tpl-select-sm' data-var='arch'><option value='amd64'>amd64</option><option value='arm64'>arm64</option><option value='386'>386</option><option value='arm'>arm</option></select></span><copy-btn></copy-btn></td>";
                const prAptOneLiner = pr.is_private
                    ? 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + apt + "/" + pr.name + "/install.sh | sudo BUILDHOST_TOKEN=$TOKEN sh"
                    : "curl -fsSL " + apt + "/" + pr.name + "/install.sh | sudo sh";
                html += "<td class='endpoint-cell'><a href='" + h(apt + "/" + pr.name + "/install.sh") + "' data-copy='" + h(prAptOneLiner) + "' title='Copies the one-line install command'>" + h(apt + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + h(brew + "/" + pr.name) + "'>" + h(brew + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + h(npm + "/@buildhost/" + pr.name) + "'>" + h(npm + "/@buildhost/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "</tr>";
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content")!.innerHTML = html;
    });
}

function renderTokens(tokens: TokenInfo[], projects: ProjectSummary[], newToken: string | null): void {
    let html = "<h1>API Tokens</h1>";

    // A created token's plaintext is returned once and never stored, so it is
    // revealed here or lost.
    if (newToken) {
        html += '<div class="token-reveal"><div class="token-reveal-label">New token — copy it now, it won\'t be shown again</div>';
        html += '<div class="token-reveal-value"><code id="new-token-val">' + h(newToken) + "</code>";
        html += '<button class="btn btn-sm" onclick="App.copyText(\'new-token-val\')">Copy</button></div></div>';
    }

    let projectOpts = '<option value="">Global</option>';
    for (const p of projects) {
        projectOpts += '<option value="' + h(p.id) + '">' + h(p.name) + "</option>";
    }
    let scopeOpts = "";
    for (const [value, label] of SCOPE_OPTIONS) scopeOpts += '<option value="' + value + '">' + label + "</option>";

    html += '<div class="card"><h2>Create Token</h2>';
    html += '<form id="create-token-form" class="inline-form">';
    html += '<input class="form-input" type="text" id="tok-name" placeholder="Name" required>';
    html += '<select class="form-select" id="tok-scopes">' + scopeOpts + "</select>";
    html += '<select class="form-select" id="tok-project">' + projectOpts + "</select>";
    html += '<button class="btn btn-primary" type="submit">Create</button>';
    html += "</form></div>";

    html += '<div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Prefix</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th><th>Last Used</th><th>Expires</th><th></th></tr></thead><tbody>';
    if (tokens.length === 0) {
        html += '<tr><td colspan="9" class="empty">No tokens yet</td></tr>';
    } else {
        for (const t of tokens) {
            html += '<tr id="tok-row-' + t.id + '"' + (t.is_expired ? ' class="row-muted"' : "") + ">";
            html += '<td id="tok-row-name-' + t.id + '">' + h(t.name) + "</td>";
            html += "<td><code>" + h(t.token_prefix) + "...</code></td>";
            html += "<td>" + (t.is_global ? badge("neutral", "Global") : badge("info", "Project")) + "</td>";
            html += "<td>" + (t.project_name ? "<a href='#/projects/" + h(t.project_name) + "'>" + h(t.project_name) + "</a>" : "-") + "</td>";
            html += '<td id="tok-row-scopes-' + t.id + '"><code>' + h(t.scopes) + "</code></td>";
            html += '<td title="' + h(formatTime(t.created_at)) + '">' + h(timeAgo(t.created_at)) + "</td>";
            html += "<td>" + (t.last_used_at ? h(formatTime(t.last_used_at)) : '<span class="muted">Never</span>') + "</td>";
            let exp = "";
            if (t.expires_at) {
                if (t.is_expired) exp += badge("danger", "Expired") + " ";
                exp += h(formatTime(t.expires_at));
            } else {
                exp = '<span class="muted">Never</span>';
            }
            html += "<td>" + exp + "</td>";
            html += '<td class="row-actions"><button class="btn btn-sm" onclick="App.editToken(' + t.id + ",'" + h(t.name) + "','" + h(t.scopes) + '\')">Edit</button> ';
            html += '<button class="btn btn-sm btn-danger" onclick="App.deleteToken(' + t.id + ')">Delete</button></td>';
            html += "</tr>";
        }
    }
    html += "</tbody></table></div>";
    document.getElementById("content")!.innerHTML = html;

    const form = document.getElementById("create-token-form");
    if (form) {
        form.addEventListener("submit", (e) => {
            e.preventDefault();
            const name = (document.getElementById("tok-name") as HTMLInputElement).value.trim();
            const scopes = (document.getElementById("tok-scopes") as HTMLSelectElement).value;
            const projVal = (document.getElementById("tok-project") as HTMLSelectElement).value;
            const body: { name: string; scopes: string; project_id?: number } = { name, scopes };
            if (projVal) body.project_id = parseInt(projVal, 10);
            fetch("/api/tokens", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(body),
            }).then((r) => {
                if (!r.ok) return r.text().then((t) => { alert("Error: " + t); });
                return r.json().then((d: { token: string }) => reloadTokens(d.token));
            });
        });
    }
}

function reloadTokens(newToken?: string): void {
    Promise.all([apiFetch<TokenInfo[]>("/tokens"), apiFetch<ProjectSummary[]>("/projects")]).then(([tokens, projects]) => {
        renderTokens(tokens, projects, newToken || null);
    });
}

function pageTokens(): void {
    setTitle("Tokens");
    renderSidebar("tokens");
    reloadTokens();
}

function pageSites(): void {
    setTitle("Sites");
    renderSidebar("sites");
    apiFetch<SitesData>("/sites").then((d) => {
        const bu = d.base_url || "";
        const sitesBase = (d.services || ({} as Partial<ServiceURLs>)).sites || "";
        const sites = d.sites || [];

        const byProject: Record<string, ProjectGroupInfo> = {};
        for (const s of sites) {
            if (!byProject[s.project_name]) {
                byProject[s.project_name] = { branches: 0, total_size: 0, total_files: 0, last_updated: s.updated_at };
            }
            const p = byProject[s.project_name];
            p.branches++;
            p.total_size += s.size || 0;
            p.total_files += s.file_count || 0;
            if (s.updated_at > p.last_updated) p.last_updated = s.updated_at;
        }

        const names = Object.keys(byProject).sort();
        let html = '<h1>Static Sites</h1><div class="card"><table class="data-table"><thead><tr><th>Project</th><th>Branches</th><th>Files</th><th>Total Size</th><th>Last Updated</th></tr></thead><tbody>';
        if (names.length === 0) {
            html += '<tr><td colspan="5" class="empty">No sites deployed</td></tr>';
        } else {
            for (const name of names) {
                const info = byProject[name];
                html += "<tr><td><a href='#/sites/" + h(name) + "'>" + h(name) + "</a></td>";
                html += "<td>" + info.branches + "</td>";
                html += "<td>" + info.total_files + "</td>";
                html += "<td>" + h(humanSize(info.total_size)) + "</td>";
                html += '<td title="' + h(formatTime(info.last_updated)) + '">' + h(timeAgo(info.last_updated)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        html += '<div class="card"><h2>Deploy a Site</h2>';
        html += codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project {project} \\\n  --branch {branch} \\\n  --dir ./dist");
        html += codeBlock("curl", 'tar czf - -C ./dist . | curl -X PUT \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/gzip" \\\n  --data-binary @- \\\n  ' + sitesBase + "/{project}/branch/{branch}");
        html += "</div>";

        document.getElementById("content")!.innerHTML = html;
    });
}

function pageSite(name: string): void {
    setTitle(name + " - Sites");
    renderSidebar("sites");
    apiFetch<ProjectData>("/projects/" + encodeURIComponent(name)).then((d) => {
        const p = d.project;
        const bu = d.base_url || "";
        const sitesBase = (d.services || ({} as Partial<ServiceURLs>)).sites || "";
        const sites = d.sites || [];

        let html = '<h1><a href="#/sites">Sites</a> / ' + h(p.name) + "</h1>";

        html += '<div class="card"><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
        if (sites.length === 0) {
            html += '<tr><td colspan="6" class="empty">No branches deployed</td></tr>';
        } else {
            for (const s of sites) {
                html += "<tr><td><code>" + h(s.branch) + "</code></td>";
                html += "<td>" + s.file_count + "</td>";
                html += "<td>" + h(humanSize(s.size)) + "</td>";
                html += "<td>" + (s.git_commit ? '<code class="commit">' + h(s.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + h(formatTime(s.updated_at)) + '">' + h(timeAgo(s.updated_at)) + "</td>";
                html += '<td><a href="' + h(sitesBase + "/" + p.name + "/branch/" + s.branch + "/") + '" target="_blank">Open</a></td></tr>';
            }
        }
        html += "</tbody></table></div>";

        html += '<div class="card"><h2>Deploy to ' + h(p.name) + "</h2>";
        html += codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project " + p.name + " \\\n  --branch {branch} \\\n  --dir ./dist");
        html += codeBlock("Delete a branch", 'curl -X DELETE \\\n  -H "Authorization: Bearer $TOKEN" \\\n  ' + bu + "/sites/" + p.name + "/branch/{branch}");
        html += "</div>";

        document.getElementById("content")!.innerHTML = html;
    });
}

function pageOIDC(): void {
    setTitle("OIDC Policies");
    renderSidebar("oidc");
    apiFetch<OIDCPolicy[]>("/oidc").then((policies) => {
        let html = '<h1>OIDC Policies</h1><div class="card"><table class="data-table"><thead><tr><th>Issuer</th><th>Subject Pattern</th><th>Audience</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th></tr></thead><tbody>';
        if (policies.length === 0) {
            html += '<tr><td colspan="7" class="empty">No OIDC policies configured</td></tr>';
        } else {
            for (const p of policies) {
                html += "<tr><td class='truncate'><code>" + h(p.issuer) + "</code></td>";
                html += "<td><code>" + h(p.subject_pattern) + "</code></td>";
                html += "<td>" + (p.audience ? "<code>" + h(p.audience) + "</code>" : '<span class="muted">Any</span>') + "</td>";
                html += "<td>" + (p.project_name ? badge("info", "Project") : badge("neutral", "Global")) + "</td>";
                html += "<td>" + (p.project_name ? "<a href='#/projects/" + h(p.project_name) + "'>" + h(p.project_name) + "</a>" : "-") + "</td>";
                html += "<td><code>" + h(p.scopes) + "</code></td>";
                html += '<td title="' + h(formatTime(p.created_at)) + '">' + h(timeAgo(p.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
}

function pageArtifacts(): void {
    setTitle("Artifacts");
    renderSidebar("dashboard");
    apiFetch<AllArtifact[]>("/artifacts").then((artifacts) => {
        let html = '<h1>All Artifacts</h1><div class="card"><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Platform</th><th>Kind</th><th>Filename</th><th>Size</th><th>Downloads</th><th>Created</th></tr></thead><tbody>';
        if (artifacts.length === 0) {
            html += '<tr><td colspan="8" class="empty">No artifacts yet</td></tr>';
        } else {
            for (const a of artifacts) {
                html += "<tr><td><a href='#/projects/" + h(a.project_name) + "'>" + h(a.project_name) + "</a></td>";
                html += "<td><a href='#/projects/" + h(a.project_name) + "/releases/" + h(a.version) + "'><code>" + h(a.version) + "</code></a></td>";
                html += "<td>" + badge("info", a.os + "/" + a.arch) + "</td>";
                html += "<td>" + badge("neutral", a.kind) + "</td>";
                html += "<td>" + (a.filename ? "<code>" + h(a.filename) + "</code>" : '<span class="muted">-</span>') + "</td>";
                html += "<td>" + h(humanSize(a.size)) + "</td>";
                html += "<td>" + a.download_count + "</td>";
                html += '<td title="' + h(formatTime(a.created_at)) + '">' + h(timeAgo(a.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
}

function pageStorage(): void {
    setTitle("Storage");
    renderSidebar("dashboard");
    apiFetch<StorageData>("/storage").then((d) => {
        const projects = d.projects || [];
        let html = '<h1>Storage Usage</h1><div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.total_bytes || 0)) + '</div><div class="stat-label">Artifact Storage</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.logical_bytes || 0)) + '</div><div class="stat-label">Logical Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.physical_bytes || 0)) + '</div><div class="stat-label">Physical Size (dedup)</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_bytes || 0)) + '</div><div class="stat-label">Blobs on Disk</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.reclaimable_bytes || 0)) + '</div><div class="stat-label">Reclaimable (est.)</div></div>';
        if (d.disk_total) {
            html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_used || 0)) + " / " + h(humanSize(d.disk_total || 0)) + '</div><div class="stat-label">Filesystem Usage</div></div>';
        }
        html += "</div>";
        // The five storage numbers above only make sense as one arithmetic chain:
        // uploads + generated blobs = logical, minus dedup = physical, minus
        // compression = what the blob store actually holds.
        const logical = d.logical_bytes || 0;
        const physical = d.physical_bytes || 0;
        const disk = d.disk_bytes || 0;
        const bdRow = (op: string, name: string, bytes: number, cls: string): string =>
            "<tr" + (cls ? ' class="' + cls + '"' : "") +
            '><td class="bd-op">' + op + '</td><td class="bd-name">' + name +
            '</td><td class="bd-val">' + h(humanSize(bytes)) + "</td></tr>";

        html += '<div class="card"><h2>How these reconcile</h2>';
        html += '<table class="breakdown-table"><tbody>';
        html += bdRow("", "Original uploads", d.total_bytes || 0, "");
        html += bdRow("+", "Packaged artifacts", d.packaged_bytes || 0, "");
        html += bdRow("+", "Stripped binaries", d.stripped_bytes || 0, "");
        html += bdRow("+", "Debug symbols", d.debug_bytes || 0, "");
        html += bdRow("=", "Logical (all references)", logical, "bd-subtotal");
        html += bdRow("−", "Deduplication", Math.max(0, logical - physical), "bd-saving");
        html += bdRow("=", "Physical (unique blobs)", physical, "bd-subtotal");
        html += bdRow("−", "Compression (zstd)", Math.max(0, physical - disk), "bd-saving");
        html += bdRow("=", "Blobs on disk", disk, "bd-total");
        html += "</tbody></table>";
        html += '<p class="breakdown-note">Original uploads counts only the binaries you pushed. Packaged, stripped, and debug blobs are produced on demand but still stored, so they occupy disk yet are excluded from "Artifact Storage" above. Deduplication and zstd compression then reduce the unique set to what the blob store actually holds.</p>';
        html += "</div>";

        html += '<div class="card"><h2>Per-Project Breakdown</h2><table class="data-table"><thead><tr><th>Project</th><th>Releases</th><th>Artifacts</th><th>Total Size</th></tr></thead><tbody>';
        if (projects.length === 0) {
            html += '<tr><td colspan="4" class="empty">No projects yet</td></tr>';
        } else {
            for (const p of projects) {
                html += "<tr><td><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a></td>";
                html += "<td>" + p.release_count + "</td>";
                html += "<td>" + p.artifact_count + "</td>";
                html += "<td>" + h(humanSize(p.total_bytes)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
}

// --- Clipboard, downloads, and temporary links ---

function copyText(elemId: string): void {
    const el = document.getElementById(elemId);
    if (!el) return;
    navigator.clipboard.writeText(el.textContent || "");
}

// copyTempLink mints a temporary, artifact-bound download link via the admin API
// and copies it to the clipboard. The link carries a signed &token= that works
// even for a private project (unlike the plain dl links), expiring in 1h.
function copyTempLink(btn: HTMLElement, project: string, version: string, os: string, arch: string, fmt: string): void {
    if (demo) return;
    const orig = btn.textContent;
    const restore = (msg: string): void => {
        btn.textContent = msg;
        setTimeout(() => {
            btn.textContent = orig;
            btn.classList.remove("copied");
            (btn as HTMLButtonElement).disabled = false;
        }, 2000);
    };
    (btn as HTMLButtonElement).disabled = true;
    btn.textContent = "...";
    fetch("/api/projects/" + project + "/download-links", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ os, arch, version, fmt }),
    }).then((r) => {
        if (!r.ok) return r.text().then((t) => { throw new Error(t || String(r.status)); });
        return r.json();
    }).then((d: { url: string }) => navigator.clipboard.writeText(d.url).then(() => {
        btn.classList.add("copied");
        restore("copied 1h link");
    })).catch(() => restore("failed"));
}

// dlMintLink renders a download link for a private project's artifact. A plain dl
// link would 401, so this one mints a signed single-artifact link on click and
// downloads it. Values are safe charsets (project/version/os/arch/fmt), so they
// embed directly in the inline handler.
function dlMintLink(project: string, version: string, os: string, arch: string, fmt: string, debug: boolean, label: string, title: string): string {
    const call = "App.downloadArtifact(this,'" + project + "','" + version + "','" + os + "','" + arch + "','" + fmt + "'," + (debug ? "true" : "false") + ")";
    return '<a href="#" class="dl-mint" onclick="return ' + h(call) + '" title="' + h(title) + '">' + h(label) + "</a>";
}

// downloadArtifact mints a temporary signed link for exactly this artifact, then
// triggers the download by clicking a synthetic anchor (same effect as following a
// normal download link). Returns false so the placeholder href="#" is not used.
function downloadArtifact(el: HTMLElement | null, project: string, version: string, os: string, arch: string, fmt: string, debug: boolean): boolean {
    if (demo) return false;
    const orig = el ? el.textContent : "";
    fetch("/api/projects/" + project + "/download-links", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ os, arch, version, fmt, debug: !!debug }),
    }).then((r) => {
        if (!r.ok) return r.text().then((t) => { throw new Error(t || String(r.status)); });
        return r.json();
    }).then((d: { url: string }) => {
        const a = document.createElement("a");
        a.href = d.url;
        a.rel = "noopener";
        document.body.appendChild(a);
        a.click();
        a.remove();
    }).catch(() => {
        if (el) {
            el.textContent = "failed";
            setTimeout(() => { el.textContent = orig; }, 2000);
        }
    });
    return false;
}

// --- Token editing ---

const SCOPE_OPTIONS: Array<[string, string]> = [
    ["read,write", "read+write"],
    ["read", "read"],
    ["write", "write"],
    ["share", "share"],
    ["read,write,share", "read+write+share"],
];

function editToken(id: number, name: string, scopes: string): void {
    const nameCell = document.getElementById("tok-row-name-" + id);
    const scopesCell = document.getElementById("tok-row-scopes-" + id);
    const row = document.getElementById("tok-row-" + id);
    if (!nameCell || !scopesCell) return;

    nameCell.innerHTML = '<input class="form-input form-input-sm" type="text" id="edit-name-' + id + '" value="' + h(name) + '">';
    let sel = '<select class="form-select form-select-sm" id="edit-scopes-' + id + '">';
    for (const [value, label] of SCOPE_OPTIONS) {
        sel += '<option value="' + value + '"' + (scopes === value ? " selected" : "") + ">" + label + "</option>";
    }
    scopesCell.innerHTML = sel + "</select>";

    const actionsCell = row ? row.querySelector(".row-actions") : null;
    if (actionsCell) {
        actionsCell.innerHTML = '<button class="btn btn-sm btn-primary" onclick="App.saveToken(' + id + ')">Save</button> ' +
            '<button class="btn btn-sm" onclick="App.pages.tokens._reload()">Cancel</button>';
    }
}

function saveToken(id: number): void {
    const nameEl = document.getElementById("edit-name-" + id) as HTMLInputElement | null;
    const scopesEl = document.getElementById("edit-scopes-" + id) as HTMLSelectElement | null;
    if (!nameEl || !scopesEl) return;
    fetch("/api/tokens/" + id, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: nameEl.value.trim(), scopes: scopesEl.value }),
    }).then((r) => {
        if (!r.ok) return r.text().then((t) => { alert("Error: " + t); });
        reloadTokens();
    });
}

function deleteToken(id: number): void {
    if (!confirm("Delete this token? This cannot be undone.")) return;
    fetch("/api/tokens/" + id, { method: "DELETE" }).then((r) => {
        if (!r.ok) return r.text().then((t) => { alert("Error: " + t); });
        reloadTokens();
    });
}

// --- Retention ---

function pageRetention(): void {
    setTitle("Retention");
    renderSidebar("retention");
    apiFetch<RetentionData>("/retention").then(renderRetention);
}

function renderRetention(d: RetentionData): void {
    const p = d.preview || ({} as RetentionData["preview"]);
    const rels = p.releases || [];

    let sweeper: string;
    if (!d.sweeper_enabled) sweeper = badge("neutral", "Background sweeper: off");
    else if (d.sweeper_enforce) sweeper = badge("danger", "Background sweeper: ON — deleting automatically");
    else sweeper = badge("warning", "Background sweeper: ON — report-only");

    let html = "<h1>Retention &amp; Garbage Collection</h1>";
    html += '<div class="card"><p class="muted">Eviction keeps the newest <strong>keep-N</strong> published releases on each <code>(project, branch)</code> and sweeps abandoned uploads, then deletes any blob nothing else references. Each branch&#39;s latest published build, tagged images, pushed-docker builds, and anything inside the recency guard are <strong>never</strong> deleted. Nothing happens until you run it below (or enable the background sweeper via env).</p><p>' + sweeper + "</p></div>";

    html += '<div class="card"><h2>Policy</h2><form id="retention-form">';
    html += '<div style="display:flex;gap:24px;flex-wrap:wrap;align-items:flex-end">';
    html += '<label>Keep per branch<br><input class="form-input form-input-sm" type="number" id="ret-keepn" min="0" max="100000" value="' + h(d.keep_n) + '"></label>';
    html += '<label>Recency guard (hours)<br><input class="form-input form-input-sm" type="number" id="ret-recency" min="0" max="87600" value="' + h(d.recency_hours) + '"></label>';
    html += '<button class="btn btn-primary" type="submit">Save policy</button>';
    html += "</div></form></div>";

    html += '<div class="card"><h2>Preview <span class="muted" style="font-weight:400">— what enforcing now would delete</span></h2>';
    html += '<div class="stat-grid">';
    html += '<div class="stat-card"><div class="stat-value">' + h(p.release_count || 0) + '</div><div class="stat-label">Releases evicted</div></div>';
    html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(p.reclaimable_bytes || 0)) + '</div><div class="stat-label">Reclaimable</div></div>';
    html += '<div class="stat-card"><div class="stat-value">' + h(p.blobs || 0) + '</div><div class="stat-label">Blobs freed</div></div>';
    html += '<div class="stat-card"><div class="stat-value">' + h(p.blobs_retained || 0) + '</div><div class="stat-label">Shared blobs kept</div></div>';
    html += "</div>";

    html += '<table class="data-table"><thead><tr><th>Project</th><th>Branch</th><th>Version</th><th>Reason</th></tr></thead><tbody>';
    if (rels.length === 0) {
        html += '<tr><td colspan="4" class="empty">Nothing to evict under the current policy</td></tr>';
    } else {
        for (const r of rels) {
            const proj = r.project_name ? "<a href='#/projects/" + h(r.project_name) + "'>" + h(r.project_name) + "</a>" : h("project " + r.project_id);
            html += "<tr><td>" + proj + "</td>";
            html += "<td>" + (r.branch ? h(r.branch) : '<span class="muted">(none)</span>') + "</td>";
            html += "<td>" + h(r.version) + "</td>";
            html += "<td>" + badge(r.reason === "abandoned" ? "neutral" : "info", r.reason) + "</td></tr>";
        }
    }
    html += "</tbody></table>";
    html += '<div class="row-actions" style="margin-top:16px">';
    html += '<button class="btn" onclick="App.pages.retention()">Refresh preview</button> ';
    html += '<button class="btn btn-danger" onclick="App.runRetention()">Run garbage collection now</button>';
    html += "</div></div>";

    document.getElementById("content")!.innerHTML = html;

    const form = document.getElementById("retention-form");
    if (form) {
        form.addEventListener("submit", (e) => {
            e.preventDefault();
            const keepN = parseInt((document.getElementById("ret-keepn") as HTMLInputElement).value, 10);
            const recency = parseInt((document.getElementById("ret-recency") as HTMLInputElement).value, 10);
            fetch("/api/retention", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ keep_n: keepN, recency_hours: recency }),
            }).then((res) => {
                if (!res.ok) return res.text().then((t) => { alert("Error: " + t); });
                return res.json().then(renderRetention);
            }).catch(() => { alert("Could not save policy (preview/demo mode has no backend)."); });
        });
    }
}

function runRetention(): void {
    if (!confirm("Permanently delete the releases shown in the preview and reclaim their storage? This cannot be undone.")) return;
    fetch("/api/retention/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enforce: true }),
    }).then((res) => {
        if (!res.ok) return res.text().then((t) => { alert("Error: " + t); });
        return res.json().then((rep: { release_count?: number; reclaimable_bytes?: number; blobs?: number }) => {
            alert("Garbage collection complete: evicted " + (rep.release_count || 0) + " releases, freed " +
                humanSize(rep.reclaimable_bytes || 0) + " across " + (rep.blobs || 0) + " blobs.");
            pageRetention();
        });
    }).catch(() => { alert("Could not run GC (preview/demo mode has no backend)."); });
}

// --- Router ---

// Rendered links carry raw project names, but a hand-written URL may
// percent-encode them. Decode when it is valid and fall back to the raw text
// when it is not, so a stray % cannot throw the router.
function decodeSegment(s: string): string {
    try {
        return decodeURIComponent(s);
    } catch {
        return s;
    }
}

// Project names are slash-namespaced (`repo/binary`, to any depth), so the
// project and release patterns match greedily across slashes. Splitting the hash
// into a fixed number of segments would strand every namespaced project.
function route(): void {
    const hash = window.location.hash.replace(/^#\/?/, "") || "";

    const releaseM = hash.match(/^projects\/(.+)\/releases\/([^/]+)$/);
    if (releaseM) { pageRelease(decodeSegment(releaseM[1]), decodeSegment(releaseM[2])); return; }

    const projectM = hash.match(/^projects\/(.+)$/);
    if (projectM) { pageProject(decodeSegment(projectM[1])); return; }

    const siteM = hash.match(/^sites\/(.+)$/);
    if (siteM) { pageSite(decodeSegment(siteM[1])); return; }

    const first = hash.split("/")[0];
    if (first === "projects") pageProjects();
    else if (first === "registries") pageRegistries();
    else if (first === "sites") pageSites();
    else if (first === "tokens") pageTokens();
    else if (first === "oidc") pageOIDC();
    else if (first === "artifacts") pageArtifacts();
    else if (first === "storage") pageStorage();
    else if (first === "retention") pageRetention();
    else pageDashboard();
}

// --- Demo data ---

// Demo mode renders the dashboard with no backend (the API is unreachable, e.g.
// the static preview build), so every payload here carries the same shape the
// server sends -- including `services`, without which every endpoint the pages
// render would come out blank.
const demoServices: ServiceURLs = {
    dl: "https://dl.builds.example.com",
    apt: "https://apt.builds.example.com",
    brew: "https://brew.builds.example.com",
    npm: "https://npm.builds.example.com",
    oci: "https://oci.builds.example.com",
    sites: "https://sites.builds.example.com",
    static: "https://static.builds.example.com",
};

const demoData: Record<string, unknown> = {
    "/sidebar": { build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" }, build_age: "", cpu_percent: "0.0%", disk_used: "0 B", disk_total: "0 B" },
    "/dashboard": {
        stats: { project_count: 2, release_count: 5, artifact_count: 12, total_storage_bytes: 52428800, token_count: 3, site_count: 3 },
        recent: [
            { project_name: "myapp", version: "3", git_branch: "main", published: true, created_at: new Date(Date.now() - 3600000).toISOString() },
            { project_name: "cli-tool", version: "1.2.0", git_branch: "release", published: true, created_at: new Date(Date.now() - 86400000).toISOString() },
        ],
        config: { base_url: "https://builds.example.com", listen_addr: ":8080", admin_listen_addr: ":9090", data_dir: "./data", oidc_issuers: ["https://token.actions.githubusercontent.com"], oidc_orgs: ["myorg"], oidc_events: ["push"] },
        services: demoServices,
        build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" },
        uptime: "0m 0s", cpu_percent: "0.0%", cpu_total: "0m 0s",
    },
    "/projects": [
        { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, release_count: 3, artifact_count: 8, created_at: new Date(Date.now() - 864e5 * 30).toISOString() },
        { id: 2, name: "cli-tool", description: "CLI utility", versioning: "semver", is_private: true, release_count: 2, artifact_count: 4, created_at: new Date(Date.now() - 864e5 * 10).toISOString() },
    ],
    "/projects/myapp": {
        project: { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, created_at: new Date(Date.now() - 864e5 * 30).toISOString(), updated_at: new Date(Date.now() - 3600000).toISOString() },
        releases: [{ version: "3", git_branch: "main", git_commit: "abc123", published: true, artifact_count: 4, published_at: new Date(Date.now() - 3600000).toISOString(), created_at: new Date(Date.now() - 3600000).toISOString() }],
        sites: [{ branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }],
        base_url: "https://builds.example.com",
        services: demoServices,
    },
    "/registries": { base_url: "https://builds.example.com", services: demoServices, projects: [{ name: "myapp", is_private: false }, { name: "cli-tool", is_private: true }] },
    "/sites": { sites: [{ project_name: "myapp", branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { project_name: "myapp", branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }, { project_name: "cli-tool", branch: "main", file_count: 8, size: 23000, git_commit: "fff000111222", updated_at: new Date(Date.now() - 86400000).toISOString() }], base_url: "https://builds.example.com", services: demoServices },
    "/tokens": [{ id: 1, name: "deploy", token_prefix: "bh_abc", is_global: false, project_id: 1, project_name: "myapp", scopes: "read,write", is_expired: false, created_at: new Date(Date.now() - 864e5 * 7).toISOString(), last_used_at: new Date(Date.now() - 3600000).toISOString() }],
    "/oidc": [{ issuer: "https://token.actions.githubusercontent.com", subject_pattern: "repo:myorg/myapp:*", audience: "", project_name: "myapp", scopes: "read,write", created_at: new Date(Date.now() - 864e5 * 14).toISOString() }],
    "/artifacts": [
        { id: 1, os: "linux", arch: "amd64", kind: "binary", size: 15728640, filename: "myapp", created_at: new Date(Date.now() - 3600000).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 42 },
        { id: 2, os: "darwin", arch: "arm64", kind: "binary", size: 14680064, filename: "myapp", created_at: new Date(Date.now() - 3600000).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 18 },
        { id: 3, os: "linux", arch: "amd64", kind: "binary", size: 10485760, filename: "cli-tool", created_at: new Date(Date.now() - 86400000).toISOString(), version: "1.2.0", git_branch: "release", project_name: "cli-tool", download_count: 7 },
    ],
    "/storage": {
        projects: [
            { id: 1, name: "myapp", total_bytes: 45000000, artifact_count: 8, release_count: 3 },
            { id: 2, name: "cli-tool", total_bytes: 7428800, artifact_count: 4, release_count: 2 },
        ],
        total_bytes: 52428800, logical_bytes: 58000000, physical_bytes: 48000000, disk_bytes: 50000000,
        disk_used: 120000000, disk_total: 500000000,
    },
    "/retention": {
        keep_n: 10, recency_hours: 24, sweeper_enabled: false, sweeper_enforce: false,
        preview: {
            enforced: false, release_count: 3, keep_n_count: 2, abandoned_count: 1,
            blobs: 4, blobs_retained: 1, reclaimable_bytes: 18874368,
            releases: [
                { project_name: "myapp", project_id: 1, branch: "main", version: "7", reason: "keep-n" },
                { project_name: "myapp", project_id: 1, branch: "main", version: "8", reason: "keep-n" },
                { project_name: "cli-tool", project_id: 2, branch: "feature-x", version: "3", reason: "abandoned" },
            ],
        },
    },
};

// --- Global handle ---

// Rendered markup wires buttons and links with inline onclick="App.…", so these
// have to be reachable from the page. The bundle is an IIFE, which otherwise
// keeps every function module-scoped and every one of those handlers dead.
// `pages` keeps the shape the rendered handlers reference (App.pages.retention(),
// App.pages.tokens._reload()) so the markup is identical to what shipped.
const pages = {
    retention: pageRetention,
    tokens: Object.assign(pageTokens, { _reload: reloadTokens }),
};

const App = {
    pages,
    projectTreeRows,
    projectLabel,
    projectNameCell,
    copyText,
    copyTempLink,
    dlMintLink,
    downloadArtifact,
    editToken,
    saveToken,
    deleteToken,
    reloadTokens,
    pageRetention,
    runRetention,
};

declare global {
    interface Window { App: typeof App }
}
window.App = App;

// --- Init ---

document.addEventListener("DOMContentLoaded", () => {
    if (window.location.pathname !== "/") demo = true;
    apiFetch<SidebarData>("/sidebar").then((data) => {
        sidebarCache = data;
        route();
    });
});

window.addEventListener("hashchange", () => {
    route();
});
