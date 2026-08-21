// The admin dashboard SPA.
//
// esbuild bundles this as an IIFE with --global-name=App, so every name EXPORTED
// at the bottom lands on window.App -- which is what the inline
// onclick="App.x(...)" handlers in the HTML generated here call. Adding an
// inline handler for a function means exporting it too, or the button is dead.

import { Html, type El } from "./html.ts";

import type {
    AllArtifact,
    DashboardData,
    DownloadLink,
    GoproxyData,
    OIDCPolicy,
    Pages,
    Platform,
    ProjectData,
    ProjectSummary,
    RegistriesData,
    ReleaseData,
    RetentionData,
    SidebarData,
    SitesData,
    StorageData,
    TokenInfo,
    TreeRow,
} from "./types.ts";


let demo = false;
let sidebarCache: SidebarData | null = null;

const h = function (s: string | number | null | undefined): string {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
};

// siteBranchURL links to a site's deployment. "@" is the read grammar; the
// "/branch/" spelling only redirects here. The default branch's shorter bare
// URL needs server state the admin API does not carry, and "@" reaches it in
// one hop, so this names the branch either way.
const siteBranchURL = function (sitesBase: string, project: string, branch: string): string {
    return sitesBase + "/" + project + "/@" + branch + "/";
};

const humanSize = function (b: number): string {
    if (b < 1024) return b + " B";
    var units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
    var i = -1;
    var v = b;
    do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
    return v.toFixed(1) + " " + units[i];
};

const timeAgo = function (s: string | null | undefined): string {
    if (!s) return "-";
    var d = Date.now() - new Date(s).getTime();
    if (d < 60000) return "just now";
    var m = Math.floor(d / 60000);
    if (m < 60) return m === 1 ? "1 minute ago" : m + " minutes ago";
    var h = Math.floor(m / 60);
    if (h < 24) return h === 1 ? "1 hour ago" : h + " hours ago";
    var days = Math.floor(h / 24);
    return days === 1 ? "1 day ago" : days + " days ago";
};

// The browser reports the zone, so the reader never converts a timestamp by
// hand. Intl gives a name like "PDT". The fallback builds the offset itself.
const zoneLabel = function (d: Date): string {
    try {
        const parts = new Intl.DateTimeFormat(undefined, { timeZoneName: "short" }).formatToParts(d);
        for (const p of parts) {
            if (p.type === "timeZoneName" && p.value) return p.value;
        }
    } catch (e) { /* use the offset below */ }
    const off = -d.getTimezoneOffset();
    const abs = Math.abs(off);
    const mm = abs % 60;
    return "UTC" + (off < 0 ? "-" : "+") + Math.floor(abs / 60) + (mm ? ":" + (mm < 10 ? "0" + mm : "" + mm) : "");
};

const formatTime = function (s: string | null | undefined): string {
    if (!s) return "-";
    var d = new Date(s);
    if (isNaN(d.getTime())) return "-";
    var pad = function (n: number): string { return n < 10 ? "0" + n : "" + n; };
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
        " " + pad(d.getHours()) + ":" + pad(d.getMinutes()) + " " + zoneLabel(d);
};

const apiFetch = function <T>(path: string): Promise<T> {
    if (demo) return Promise.resolve((demoData[path] || {}) as T);
    return fetch("/api" + path).then(function (r) {
        if (!r.ok) throw new Error(String(r.status));
        return r.json();
    }).catch(function () {
        demo = true;
        return (demoData[path] || {}) as T;
    });
};

const setTitle = function (t: string): void {
    document.title = t + " - Buildhost Admin";
};

var NAV_ITEMS = [
    { id: "dashboard", href: "#/", label: "Dashboard", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M10.707 2.293a1 1 0 00-1.414 0l-7 7a1 1 0 001.414 1.414L4 10.414V17a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 011-1h2a1 1 0 011 1v2a1 1 0 001 1h2a1 1 0 001-1v-6.586l.293.293a1 1 0 001.414-1.414l-7-7z"/></svg>' },
    { id: "projects", href: "#/projects", label: "Projects", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>' },
    { id: "registries", href: "#/registries", label: "Registries", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4 4a2 2 0 012-2h8a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V4zm2 0h8v3H6V4zm0 5h8v2H6V9zm0 4h5v2H6v-2z" clip-rule="evenodd"/></svg>' },
    { id: "tokens", href: "#/tokens", label: "Tokens", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M18 8a6 6 0 01-7.743 5.743L10 14l-1 1-1 1H6v2H2v-4l4.257-4.257A6 6 0 1118 8zm-6-4a1 1 0 100 2 2 2 0 012 2 1 1 0 102 0 4 4 0 00-4-4z" clip-rule="evenodd"/></svg>' },
    { id: "sites", href: "#/sites", label: "Sites", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4.083 9h1.946c.089-1.546.383-2.97.837-4.118A6.004 6.004 0 004.083 9zM10 2a8 8 0 100 16 8 8 0 000-16zm0 2c-.076 0-.232.032-.465.262-.238.234-.497.623-.737 1.182-.389.907-.673 2.142-.766 3.556h3.936c-.093-1.414-.377-2.649-.766-3.556-.24-.56-.5-.948-.737-1.182C10.232 4.032 10.076 4 10 4zm3.971 5c-.089-1.546-.383-2.97-.837-4.118A6.004 6.004 0 0115.917 9h-1.946zm-2.003 2H8.032c.093 1.414.377 2.649.766 3.556.24.56.5.948.737 1.182.233.23.389.262.465.262.076 0 .232-.032.465-.262.238-.234.497-.623.737-1.182.389-.907.673-2.142.766-3.556zm1.166 4.118c.454-1.147.748-2.572.837-4.118h1.946a6.004 6.004 0 01-2.783 4.118zm-6.268 0C6.412 13.97 6.118 12.546 6.029 11H4.083a6.004 6.004 0 002.783 4.118z" clip-rule="evenodd"/></svg>' },
    { id: "goproxy", href: "#/goproxy", label: "Go Proxy", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M12.316 3.051a1 1 0 01.633 1.265l-4 12a1 1 0 11-1.898-.632l4-12a1 1 0 011.265-.633zM5.707 6.293a1 1 0 010 1.414L3.414 10l2.293 2.293a1 1 0 11-1.414 1.414l-3-3a1 1 0 010-1.414l3-3a1 1 0 011.414 0zm8.586 0a1 1 0 011.414 0l3 3a1 1 0 010 1.414l-3 3a1 1 0 11-1.414-1.414L16.586 10l-2.293-2.293a1 1 0 010-1.414z" clip-rule="evenodd"/></svg>' },
    { id: "oidc", href: "#/oidc", label: "OIDC Policies", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>' },
    { id: "retention", href: "#/retention", label: "Retention", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>' }
];

const renderSidebar = function (nav: string): void {
    var sb = sidebarCache;
    var build = sb ? sb.build : null;
    var links = "";
    for (var i = 0; i < NAV_ITEMS.length; i++) {
        var n = NAV_ITEMS[i]!;
        links += '<li><a href="' + n.href + '"' + (n.id === nav ? ' class="active"' : '') + '>' + n.icon + " " + h(n.label) + "</a></li>";
    }
    var footer = "";
    if (build && build.commit_url) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <a href="' + h(build!.commit_url) + '" class="sidebar-info-link">' + h(build!.short_commit) + "</a></div>";
    } else if (build && build.commit) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <span>' + h(build!.short_commit) + "</span></div>";
    }
    if (sb && sb.build_age) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Built</span> <span>' + h(sb!.build_age) + "</span></div>";
    if (sb && sb.cpu_percent) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">CPU</span> <span>' + h(sb!.cpu_percent) + "</span></div>";
    if (sb && sb.disk_total) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Disk</span> <span>' + h(sb!.disk_used) + " / " + h(sb!.disk_total) + "</span></div>";

    document.getElementById("sidebar")!.innerHTML =
        '<div class="sidebar-header"><div class="logo">B</div><div><div class="sidebar-title">Buildhost</div><div class="sidebar-subtitle">Admin Dashboard</div></div></div>' +
        '<ul class="nav-list">' + links + "</ul>" +
        '<div class="sidebar-footer">' + footer + "</div>";
};

const badge = function (type: string, text: string): string { return '<span class="badge badge-' + type + '">' + h(text) + "</span>"; };
// platformBadge renders an artifact's whole platform set as ONE badge. A file
// covering several platforms is one artifact with one download link, so listing
// it once with "APE: linux/amd64, darwin/arm64" is the honest row.
const platformBadge = function (platforms: Platform[] | undefined, exeFormat: string, os: string, arch: string): string {
    var list = platforms && platforms.length > 0
        ? platforms.map(function (p) { return p.os + "/" + p.arch; }).join(", ")
        : os + "/" + arch;
    if (exeFormat) return badge("info", exeFormat.toUpperCase() + ": " + list);
    return badge("info", list);
};

// urlTpl renders a copyable URL with inline os/arch dropdowns. `base` is the
// text before the os dropdown, `mid` the text between the os and arch dropdowns
// (e.g. "&arch=" for the query-param download URLs), and `suffix` optional text
// after. `tpl` is the full template string with {os}/{arch} placeholders that
// the copy button substitutes the selected values into.
const urlTpl = function (tpl: string, base: string, mid?: string, suffix?: string): string {
    return '<span class="url-tpl" data-tpl="' + h(tpl) + '">' +
        "<code>" + h(base) + "</code>" +
        '<select class="tpl-select" data-var="os"><option value="linux">linux</option><option value="darwin">darwin</option><option value="windows">windows</option><option value="freebsd">freebsd</option></select>' +
        "<code>" + h(mid) + "</code>" +
        '<select class="tpl-select" data-var="arch"><option value="amd64">amd64</option><option value="arm64">arm64</option><option value="386">386</option><option value="arm">arm</option></select>' +
        (suffix ? "<code>" + h(suffix) + "</code>" : "") +
        "</span><copy-btn></copy-btn>";
};

const codeBlock = function (label: string, code: string): string {
    return '<div class="code-block"><div class="code-label">' + h(label) +
        '<copy-btn class="code-copy-btn" data-src="pre"></copy-btn></div><pre>' + h(code) + "</pre></div>";
};

// statTile is the non-linking sibling of the dashboard's stat-card anchors.
const statTile = function (value: string | number, label: string): string {
    return '<div class="stat-card"><div class="stat-value">' + h(value) +
        '</div><div class="stat-label">' + h(label) + "</div></div>";
};

const projectTreeRows = function (projects: ProjectSummary[]): TreeRow[] {
    interface TreeNode { name?: string; children: Record<string, TreeNode>; project?: ProjectSummary | null }
    var root: TreeNode = { children: {} };
    var nodeFor = function (parent: TreeNode, name: string): TreeNode {
        if (!parent.children[name]) parent.children[name] = { name: name, children: {}, project: null };
        return parent.children[name]!;
    };
    for (var i = 0; i < projects.length; i++) {
        var p = projects[i]!;
        var parts = String(p.name || "").split("/");
        var cur = root;
        for (var j = 0; j < parts.length; j++) cur = nodeFor(cur, parts[j]);
        cur.project = p;
    }

    var out: TreeRow[] = [];
    var walk = function (node: TreeNode, depth: number): void {
        var names = Object.keys(node.children).sort();
        for (var i = 0; i < names.length; i++) {
            var child = node.children[names[i]!]!;
            if (Object.keys(child.children).length > 0) {
                out.push({ kind: "folder", depth: depth, name: child.name });
            }
            if (child.project) {
                out.push({ kind: "project", depth: depth, project: child.project });
            }
            walk(child, depth + 1);
        }
    };
    walk(root, 0);
    return out;
};

const projectLabel = function (name: string): string {
    var s = String(name || "");
    var i = s.lastIndexOf("/");
    return i >= 0 ? s.substring(i + 1) : s;
};

const projectNameCell = function (project: ProjectSummary, depth: number): string {
    var name = project.name || "";
    var label = projectLabel(name);
    var cell = '<span class="project-label"><a href="#/projects/' + h(name) + '">' + h(label) + "</a></span>";
    if (label !== name) cell += '<span class="project-path">' + h(name) + "</span>";
    return '<td class="project-name-cell project-depth-' + depth + '">' + cell + "</td>";
};

// --- Pages ---

const pages: Pages = {} as Pages;

pages.dashboard = function (): void {
    setTitle("Dashboard");
    renderSidebar("dashboard");
    apiFetch<DashboardData>("/dashboard").then(function (d) {
        var s = d.stats || {};
        var b = d.build || {};
        var cfg = d.config || {};
        var html = '<h1>Dashboard</h1><div class="stat-grid">';
        var cards = [
            [s.project_count, "Projects", "#/projects"],
            [s.release_count, "Releases", "#/projects"],
            [s.artifact_count, "Artifacts", "#/artifacts"],
            [humanSize(s.total_storage_bytes || 0), "Storage Used", "#/storage"],
            [s.token_count, "API Tokens", "#/tokens"],
            [s.site_count || 0, "Sites", "#/sites"]
        ];
        for (var i = 0; i < cards.length; i++) html += '<a href="' + cards[i][2] + '" class="stat-card stat-card-link"><div class="stat-value">' + h(cards[i][0]) + '</div><div class="stat-label">' + cards[i][1] + "</div></a>";
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
        var issuers = (cfg.oidc_issuers || []).map(function (v) { return "<code>" + h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Trusted OIDC Issuers</td><td>" + (issuers || '<span class="empty">None</span>') + "</td></tr>";
        var orgs = (cfg.oidc_orgs || []).map(function (v) { return "<code>" + h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Orgs</td><td>" + (orgs || '<span class="empty">None</span>') + "</td></tr>";
        var events = (cfg.oidc_events || []).map(function (v) { return "<code>" + h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Events</td><td>" + events + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Recent Releases</h2><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Branch</th><th>Status</th><th>Created</th></tr></thead><tbody>';
        var recent = d.recent || [];
        if (recent.length === 0) {
            html += '<tr><td colspan="5" class="empty">No releases yet</td></tr>';
        } else {
            for (var j = 0; j < recent.length; j++) {
                var rel = recent[j];
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
};

pages.projects = function (): void {
    setTitle("Projects");
    renderSidebar("projects");
    apiFetch<ProjectSummary[]>("/projects").then(function (projects) {
        var html = '<h1>Projects</h1><div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Description</th><th>Versioning</th><th>Visibility</th><th>Releases</th><th>Artifacts</th><th>Created</th></tr></thead><tbody>';
        if (projects.length === 0) {
            html += '<tr><td colspan="7" class="empty">No projects yet</td></tr>';
        } else {
            var rows = projectTreeRows(projects);
            for (var i = 0; i < rows.length; i++) {
                var row = rows[i];
                if (row.kind === "folder") {
                    html += '<tr class="project-folder-row"><td colspan="7" class="project-folder-cell project-depth-' + row.depth + '"><span class="project-folder-icon"></span>' + h(row.name) + "</td></tr>";
                    continue;
                }
                var p = row.project!;
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
};

pages.project = function (name: string): void {
    setTitle(name);
    renderSidebar("projects");
    apiFetch<ProjectData>("/projects/" + name).then(function (d) {
        var p = d.project;
        var svc = d.services || {};
        var rels = d.releases || [];
        var html = "<h1>" + h(p.name) + "</h1>";
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

        // Download & Install (latest): non-versioned endpoints and commands so
        // the newest build can be fetched without first opening a specific
        // release. Mirrors the release page's endpoint card, version-free, plus
        // the package-manager one-liners from the Registries page. Only shown
        // once the project has a published release ("latest" 404s otherwise).
        var hasPublished = false;
        for (var ri = 0; ri < rels.length; ri++) { if (rels[ri].published) { hasPublished = true; break; } }
        if (hasPublished) {
            var dlBase = (svc.dl || "") + "/" + p.name;
            var aptBase = (svc.apt || "") + "/" + p.name;
            var brewU = (svc.brew || "") + "/Formula/" + p.name + ".rb";
            var npmU = (svc.npm || "") + "/@buildhost/" + p.name;
            var ociU = (svc.oci || "") + "/v2/" + p.name + "/manifests/latest";
            var npmHost = (svc.npm || "").replace(/^https?:\/\//, "");
            var ociHost = (svc.oci || "").replace(/^https?:\/\//, "");
            var aptHost = (svc.apt || "").replace(/^https?:\/\//, "");
            var staticHost = (svc.static || "").replace(/^https?:\/\//, "");
            // Slash-namespaced names keep their slash in the repo URL but install
            // under a folded Debian package name (repackage.DebPackageName folds
            // '/' and '_' to '-'), so apt and dpkg agree.
            var aptPkg = p.name.replace(/[/_]/g, "-");
            var priv = !!p.is_private;

            // endpointRow: an <a> + copy-button cell for a fixed (latest)
            // service endpoint. dataCopy, when set, is what the copy button
            // yields (APT links to the Release file but copies the repo base);
            // otherwise the link text is copied.
            var endpointRow = function (label: string, text: string, href: string, dataCopy?: string | null): El {
                var link = Html.a(text).attr("href", href);
                if (dataCopy) link.attr("data-copy", dataCopy);
                return Html.tr(
                    Html.td(label).cls("info-label"),
                    Html.td(link, Html.el("copy-btn").attr("data-src", "a")).cls("endpoint-cell")
                );
            };

            // curl direct download. A private project's dl link redirects to the
            // static host, and curl only re-sends credentials across that hop
            // with --location-trusted; the token is the HTTP Basic password.
            var curlCmd = priv
                ? 'curl -fsSL --location-trusted -u "token:$TOKEN" -O \\\n  "' + dlBase + '?os=linux&arch=amd64"'
                : 'curl -fsSL -O "' + dlBase + '?os=linux&arch=amd64"';

            // APT: the server-generated install.sh is the one-line path (it
            // saves the armored key, writes the signed-by source, records the
            // token for private repos, and refreshes the index).
            var aptOneLiner = priv
                ? 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + aptBase + "/install.sh \\\n  | sudo BUILDHOST_TOKEN=$TOKEN sh"
                : "curl -fsSL " + aptBase + "/install.sh | sudo sh";

            // APT manual flow: signed-by key-import + folded Debian package
            // name, matching the Registries page and the web frontend.
            var aptCmd = priv
                ? 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + aptPkg + '.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + aptHost + ' login token password $TOKEN\nmachine ' + staticHost + ' login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install ' + aptPkg
                : 'sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + aptPkg + '.list\nsudo apt update && sudo apt install ' + aptPkg;

            var npmCmd = priv
                ? "npm config set //" + npmHost + "/:_authToken $TOKEN\nnpm install @buildhost/" + p.name + " --registry " + (svc.npm || "")
                : "npm install @buildhost/" + p.name + " --registry " + (svc.npm || "");

            var dockerCmd = priv
                ? "echo $TOKEN | docker login " + ociHost + " -u token --password-stdin\ndocker pull " + ociHost + "/" + p.name + ":latest"
                : "docker pull " + ociHost + "/" + p.name + ":latest";

            html += Html.div(
                Html.h2("Download & Install ", Html.span("— latest").cls("muted").style("font-weight:400")),
                Html.p("Always resolves to the newest published release. To pin a specific version, open a release below.").cls("section-desc"),
                Html.table(
                    Html.tr(
                        Html.td("Direct download").cls("info-label"),
                        Html.td(Html.raw(urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch="))).cls("endpoint-cell")
                    ),
                    endpointRow("APT", aptBase, aptBase + "/dists/stable/Release", aptBase),
                    endpointRow("APT installer", aptBase + "/install.sh", aptBase + "/install.sh", null),
                    endpointRow("Homebrew", brewU, brewU, null),
                    endpointRow("npm", npmU, npmU, null),
                    endpointRow("OCI", ociU, ociU, null)
                ).cls("info-table"),
                Html.raw(codeBlock("Direct download (curl)", curlCmd)),
                Html.raw(codeBlock("APT (one-line install)", aptOneLiner)),
                Html.raw(codeBlock("APT (manual setup)", aptCmd)),
                Html.raw(codeBlock("Homebrew", "brew tap pazer/build " + (svc.brew || "") + "/tap.git\nbrew trust pazer/build\nbrew install pazer/build/" + p.name)),
                Html.raw(codeBlock("npm", npmCmd)),
                Html.raw(codeBlock("Docker", dockerCmd))
            ).cls("card");
        }

        html += '<div class="card"><h2>Releases</h2><table class="data-table"><thead><tr><th>Version</th><th>Branch</th><th>Commit</th><th>Status</th><th>Artifacts</th><th>Published</th><th>Created</th></tr></thead><tbody>';
        if (rels.length === 0) {
            html += '<tr><td colspan="7" class="empty">No releases yet</td></tr>';
        } else {
            for (var i = 0; i < rels.length; i++) {
                var r = rels[i];
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

        var sites = d.sites || [];
        if (sites.length > 0) {
            var sitesBase = svc.sites || "";
            html += '<div class="card"><h2>Sites</h2><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
            for (var k = 0; k < sites.length; k++) {
                var si = sites[k];
                html += "<tr><td><code>" + h(si.branch) + "</code></td>";
                html += "<td>" + si.file_count + "</td>";
                html += "<td>" + h(humanSize(si.size)) + "</td>";
                html += "<td>" + (si.git_commit ? '<code class="commit">' + h(si.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + h(formatTime(si.updated_at)) + '">' + h(timeAgo(si.updated_at)) + "</td>";
                html += '<td><a href="' + h(siteBranchURL(sitesBase, p.name, si.branch)) + '" target="_blank">Open</a></td></tr>';
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content")!.innerHTML = html;
    });
};

pages.release = function (name: string, version: string): void {
    setTitle(name + " " + version);
    renderSidebar("projects");
    apiFetch<ReleaseData>("/projects/" + name + "/releases/" + version).then(function (d) {
        var p = d.project, r = d.release, bu = d.base_url, svc = d.services || {};
        var dlBase = (svc.dl || "") + "/" + p.name;
        var priv = !!p.is_private;
        var html = "<h1><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a> / " + h(r.version) + "</h1>";

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
        var arts = d.artifacts || [];
        if (arts.length === 0) {
            html += '<tr><td colspan="6" class="empty">No artifacts</td></tr>';
        } else {
            for (var i = 0; i < arts.length; i++) {
                var a = arts[i];
                html += "<tr><td>" + platformBadge(a.platforms, a.exe_format, a.os, a.arch) + "</td>";
                html += "<td>" + badge("neutral", a.kind) + "</td>";
                html += "<td>" + (a.filename ? "<code>" + h(a.filename) + "</code>" : '<span class="muted">-</span>') + "</td>";
                html += "<td>" + h(humanSize(a.size)) + "</td>";
                html += "<td>" + a.download_count + "</td>";
                html += '<td class="dl-links">';
                var pkgs = a.packages || [];
                if (priv) {
                    // Private project: a plain dl link would 401. Each link mints a
                    // signed, single-artifact link on click, then downloads it.
                    html += dlMintLink(p.name, r.version, a.os, a.arch, "raw", false, "raw", "Download (mints a temporary signed link)");
                    if (a.debug_storage_key) html += " " + dlMintLink(p.name, r.version, a.os, a.arch, "raw", true, "debug", "Debug symbols");
                    for (var j = 0; j < pkgs.length; j++) {
                        html += " " + dlMintLink(p.name, r.version, a.os, a.arch, pkgs[j].format, false, pkgs[j].format, pkgs[j].filename + " (" + humanSize(pkgs[j].size) + ")");
                    }
                    html += ' <button type="button" class="dl-share" onclick="App.copyTempLink(this,\'' + h(p.name) + '\',\'' + h(r.version) + '\',\'' + h(a.os) + '\',\'' + h(a.arch) + '\',\'raw\')" title="Copy a temporary 1-hour shareable link">temp link</button>';
                } else {
                    var dlQ = "?v=" + r.version + "&os=" + a.os + "&arch=" + a.arch;
                    html += '<a href="' + h(dlBase + dlQ) + '" title="Direct download">raw</a>';
                    if (a.debug_storage_key) html += ' <a href="' + h(dlBase + dlQ + "&debug=1") + '" title="Debug symbols">debug</a>';
                    for (var j = 0; j < pkgs.length; j++) {
                        html += ' <a href="' + h(dlBase + dlQ + "&fmt=" + pkgs[j].format) + '" title="' + h(pkgs[j].filename) + " (" + h(humanSize(pkgs[j].size)) + ')">' + h(pkgs[j].format) + "</a>";
                    }
                }
                html += "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        var aptU = (svc.apt || "") + "/" + p.name;
        var brewU = (svc.brew || "") + "/Formula/" + p.name + ".rb";
        var npmU = (svc.npm || "") + "/@buildhost/" + p.name;
        var ociU = (svc.oci || "") + "/v2/" + p.name + "/manifests/" + r.version;
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
};

pages.registries = function (): void {
    setTitle("Registries");
    renderSidebar("registries");
    apiFetch<RegistriesData>("/registries").then(function (d) {
        var bu = d.base_url, svc = d.services || {};
        var dl = svc.dl || "", apt = svc.apt || "", brew = svc.brew || "", npm = svc.npm || "", oci = svc.oci || "", sites = svc.sites || "", staticUrl = svc.static || "";
        var npmHost = npm.replace(/^https?:\/\//, ""), ociHost = oci.replace(/^https?:\/\//, "");
        var aptHost = apt.replace(/^https?:\/\//, ""), staticHost = staticUrl.replace(/^https?:\/\//, "");
        var html = "<h1>Registry Endpoints</h1>";

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
        html += codeBlock("Setup (public project)", 'sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ' + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\nsudo apt update && sudo apt install {project}');
        html += codeBlock("Setup (private project)", 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + aptHost + ' login token password $TOKEN\nmachine ' + staticHost + ' login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install {project}');
        html += "</div>";

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
        html += "<tr><td class='info-label'>Serve</td><td class='endpoint-cell'><code>" + h(sites + "/{project}/@{branch}/{path}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
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

        var projects = d.projects || [];
        if (projects.length > 0) {
            html += '<div class="card"><h2>Projects</h2><p class="section-desc">Quick links to project-specific endpoints.</p>';
            html += '<table class="data-table"><thead><tr><th>Project</th><th>Visibility</th><th>Direct Download</th><th>APT</th><th>Brew</th><th>npm</th></tr></thead><tbody>';
            for (var k = 0; k < projects.length; k++) {
                var pr = projects[k];
                var prDl = dl + "/" + pr.name;
                html += "<tr><td><a href='#/projects/" + h(pr.name) + "'>" + h(pr.name) + "</a></td>";
                html += "<td>" + (pr.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
                html += "<td class='endpoint-cell'><span class='url-tpl' data-tpl='" + h(prDl + "?os={os}&arch={arch}") + "'><code class='truncate'>" + h(prDl + "?os=") + "</code><select class='tpl-select tpl-select-sm' data-var='os'><option value='linux'>linux</option><option value='darwin'>darwin</option><option value='windows'>windows</option><option value='freebsd'>freebsd</option></select><code>&arch=</code><select class='tpl-select tpl-select-sm' data-var='arch'><option value='amd64'>amd64</option><option value='arm64'>arm64</option><option value='386'>386</option><option value='arm'>arm</option></select></span><copy-btn></copy-btn></td>";
                var aptOneLiner = pr.is_private
                    ? 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + apt + "/" + pr.name + "/install.sh | sudo BUILDHOST_TOKEN=$TOKEN sh"
                    : "curl -fsSL " + apt + "/" + pr.name + "/install.sh | sudo sh";
                html += "<td class='endpoint-cell'><a href='" + h(apt + "/" + pr.name + "/install.sh") + "' data-copy='" + h(aptOneLiner) + "' title='Copies the one-line install command'>" + h(apt + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + h(brew + "/" + pr.name) + "'>" + h(brew + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + h(npm + "/@buildhost/" + pr.name) + "'>" + h(npm + "/@buildhost/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "</tr>";
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content")!.innerHTML = html;
    });
};

const pageTokens = function (): void {
    setTitle("Tokens");
    renderSidebar("tokens");
    reloadTokens();
};

// reloadTokens re-fetches and re-renders the table in place, so the inline
// Save / Cancel / Delete handlers can refresh it without a route change.
const reloadTokens = function (newToken?: string | null): void {
    Promise.all([apiFetch<TokenInfo[]>("/tokens"), apiFetch<ProjectSummary[]>("/projects")] as const)
        .then(function (results) {
            renderTokensTable(results[0], results[1], newToken || null);
        });
};

function renderTokensTable(tokens: TokenInfo[], projects: ProjectSummary[], newToken: string | null): void {
        var html = '<h1>API Tokens</h1>';

        if (newToken) {
            html += '<div class="token-reveal"><div class="token-reveal-label">New token — copy it now, it won\'t be shown again</div>';
            html += '<div class="token-reveal-value"><code id="new-token-val">' + h(newToken) + '</code>';
            html += '<button class="btn btn-sm" onclick="App.copyText(\'new-token-val\')">Copy</button></div></div>';
        }

        var projectOpts = '<option value="">Global</option>';
        for (var pi = 0; pi < projects.length; pi++) {
            projectOpts += '<option value="' + h(projects[pi].id) + '">' + h(projects[pi].name) + '</option>';
        }

        html += '<div class="card"><h2>Create Token</h2>';
        html += '<form id="create-token-form" class="inline-form">';
        html += '<input class="form-input" type="text" id="tok-name" placeholder="Name" required>';
        html += '<select class="form-select" id="tok-scopes"><option value="read,write">read+write</option><option value="read">read</option><option value="write">write</option><option value="share">share</option><option value="read,write,share">read+write+share</option></select>';
        html += '<select class="form-select" id="tok-project">' + projectOpts + '</select>';
        html += '<button class="btn btn-primary" type="submit">Create</button>';
        html += '</form></div>';

        html += '<div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Prefix</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th><th>Last Used</th><th>Expires</th><th></th></tr></thead><tbody>';
        if (tokens.length === 0) {
            html += '<tr><td colspan="9" class="empty">No tokens yet</td></tr>';
        } else {
            for (var i = 0; i < tokens.length; i++) {
                var t = tokens[i];
                html += '<tr id="tok-row-' + t.id + '"' + (t.is_expired ? ' class="row-muted"' : "") + ">";
                html += '<td id="tok-row-name-' + t.id + '">' + h(t.name) + "</td>";
                html += "<td><code>" + h(t.token_prefix) + "...</code></td>";
                html += "<td>" + (t.is_global ? badge("neutral", "Global") : badge("info", "Project")) + "</td>";
                html += "<td>" + (t.project_name ? "<a href='#/projects/" + h(t.project_name) + "'>" + h(t.project_name) + "</a>" : "-") + "</td>";
                html += '<td id="tok-row-scopes-' + t.id + '"><code>' + h(t.scopes) + "</code></td>";
                html += '<td title="' + h(formatTime(t.created_at)) + '">' + h(timeAgo(t.created_at)) + "</td>";
                html += "<td>" + (t.last_used_at ? h(formatTime(t.last_used_at)) : '<span class="muted">Never</span>') + "</td>";
                var exp = "";
                if (t.expires_at) {
                    if (t.is_expired) exp += badge("danger", "Expired") + " ";
                    exp += h(formatTime(t.expires_at));
                } else {
                    exp = '<span class="muted">Never</span>';
                }
                html += "<td>" + exp + "</td>";
                html += '<td class="row-actions"><button class="btn btn-sm" onclick="App.editToken(' + t.id + ',\'' + h(t.name) + '\',\'' + h(t.scopes) + '\')">Edit</button> ';
                html += '<button class="btn btn-sm btn-danger" onclick="App.deleteToken(' + t.id + ')">Delete</button></td>';
                html += "</tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;

        var form = document.getElementById("create-token-form");
        if (form) {
            form.addEventListener("submit", function (e) {
                e.preventDefault();
                var name = (document.getElementById("tok-name") as HTMLInputElement | HTMLSelectElement).value.trim();
                var scopes = (document.getElementById("tok-scopes") as HTMLInputElement | HTMLSelectElement).value;
                var projVal = (document.getElementById("tok-project") as HTMLInputElement | HTMLSelectElement).value;
                var body: { name: string; scopes: string; project_id?: number } = { name: name, scopes: scopes };
                if (projVal) body.project_id = parseInt(projVal, 10);
                fetch("/api/tokens", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify(body)
                }).then(function (r) {
                    if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
                    return r.json().then(function (d) {
                        reloadTokens(d.token);
                    });
                });
            });
        }
}

pages.tokens = pageTokens;

const copyText = function (elemId: string): void {
    var el = document.getElementById(elemId);
    if (!el) return;
    navigator.clipboard.writeText(el.textContent || el.innerText);
};

// copyTempLink mints a temporary, artifact-bound download link via the admin API
// and copies it to the clipboard. The link carries a signed &token= that works
// even for a private project (unlike the plain dl links above), expiring in 1h.
const copyTempLink = function (btn: HTMLButtonElement, project: string, version: string, os: string, arch: string, fmt: string): void {
    if (demo) return;
    var orig = btn.textContent;
    var restore = function (msg: string): void {
        btn.textContent = msg;
        setTimeout(function () {
            btn.textContent = orig;
            btn.classList.remove("copied");
            btn.disabled = false;
        }, 2000);
    };
    btn.disabled = true;
    btn.textContent = "...";
    fetch("/api/projects/" + project + "/download-links", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ os: os, arch: arch, version: version, fmt: fmt })
    }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t || String(r.status)); });
        return r.json();
    }).then(function (d) {
        return navigator.clipboard.writeText(d.url).then(function () {
            btn.classList.add("copied");
            restore("copied 1h link");
        });
    }).catch(function () {
        restore("failed");
    });
};

// dlMintLink renders a download link for a private project's artifact. A plain dl
// link would 401, so this one mints a signed single-artifact link on click and
// downloads it. Values are safe charsets (project/version/os/arch/fmt), so they
// embed directly in the inline handler.
const dlMintLink = function (project: string, version: string, os: string, arch: string, fmt: string, debug: boolean, label: string, title: string): string {
    var call = "App.downloadArtifact(this,'" + project + "','" + version + "','" + os + "','" + arch + "','" + fmt + "'," + (debug ? "true" : "false") + ")";
    return '<a href="#" class="dl-mint" onclick="return ' + h(call) + '" title="' + h(title) + '">' + h(label) + "</a>";
};

// downloadArtifact mints a temporary signed link for exactly this artifact, then
// triggers the download by clicking a synthetic anchor (same effect as following a
// normal download link). Returns false so the placeholder href="#" is not used.
const downloadArtifact = function (el: HTMLElement | null, project: string, version: string, os: string, arch: string, fmt: string, debug: boolean): boolean {
    if (demo) return false;
    var orig = el ? el.textContent : "";
    fetch("/api/projects/" + project + "/download-links", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ os: os, arch: arch, version: version, fmt: fmt, debug: !!debug })
    }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t || String(r.status)); });
        return r.json();
    }).then(function (d) {
        var a = document.createElement("a");
        a.href = d.url;
        a.rel = "noopener";
        document.body.appendChild(a);
        a.click();
        a.remove();
    }).catch(function () {
        if (el) {
            el.textContent = "failed";
            setTimeout(function () { el.textContent = orig; }, 2000);
        }
    });
    return false;
};

const editToken = function (id: number, name: string, scopes: string): void {
    var nameCell = document.getElementById("tok-row-name-" + id);
    var scopesCell = document.getElementById("tok-row-scopes-" + id);
    var row = document.getElementById("tok-row-" + id);
    if (!nameCell || !scopesCell) return;

    nameCell.innerHTML = '<input class="form-input form-input-sm" type="text" id="edit-name-' + id + '" value="' + h(name) + '">';
    scopesCell.innerHTML = '<select class="form-select form-select-sm" id="edit-scopes-' + id + '">' +
        '<option value="read,write"' + (scopes === "read,write" ? " selected" : "") + '>read+write</option>' +
        '<option value="read"' + (scopes === "read" ? " selected" : "") + '>read</option>' +
        '<option value="write"' + (scopes === "write" ? " selected" : "") + '>write</option>' +
        '<option value="share"' + (scopes === "share" ? " selected" : "") + '>share</option>' +
        '<option value="read,write,share"' + (scopes === "read,write,share" ? " selected" : "") + '>read+write+share</option>' +
        '</select>';
    var actionsCell = row ? row.querySelector(".row-actions") : null;
    if (actionsCell) {
        actionsCell.innerHTML = '<button class="btn btn-sm btn-primary" onclick="App.saveToken(' + id + ')">Save</button> ' +
            '<button class="btn btn-sm" onclick="App.reloadTokens()">Cancel</button>';
    }
};

const saveToken = function (id: number): void {
    var nameEl = document.getElementById("edit-name-" + id) as HTMLInputElement | null;
    var scopesEl = document.getElementById("edit-scopes-" + id) as HTMLSelectElement | null;
    if (!nameEl || !scopesEl) return;
    fetch("/api/tokens/" + id, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: nameEl.value.trim(), scopes: scopesEl.value })
    }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
        reloadTokens();
    });
};

const deleteToken = function (id: number): void {
    if (!confirm("Delete this token? This cannot be undone.")) return;
    fetch("/api/tokens/" + id, { method: "DELETE" }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
        reloadTokens();
    });
};

pages.sites = function (): void {
    setTitle("Sites");
    renderSidebar("sites");
    apiFetch<SitesData>("/sites").then(function (d) {
        var bu = d.base_url || "", sitesBase = (d.services || {}).sites || "";
        var sites = d.sites || [];

        var byProject: Record<string, { branches: number; total_size: number; total_files: number; last_updated: string }> = {};
        for (var i = 0; i < sites.length; i++) {
            var s = sites[i]!;
            if (!byProject[s.project_name]) {
                byProject[s.project_name] = { branches: 0, total_size: 0, total_files: 0, last_updated: s.updated_at };
            }
            var p = byProject[s.project_name]!;
            p.branches++;
            p.total_size += s.size || 0;
            p.total_files += s.file_count || 0;
            if (s.updated_at > p.last_updated) p.last_updated = s.updated_at;
        }

        var names = Object.keys(byProject).sort();
        var html = '<h1>Static Sites</h1><div class="card"><table class="data-table"><thead><tr><th>Project</th><th>Branches</th><th>Files</th><th>Total Size</th><th>Last Updated</th></tr></thead><tbody>';
        if (names.length === 0) {
            html += '<tr><td colspan="5" class="empty">No sites deployed</td></tr>';
        } else {
            for (var j = 0; j < names.length; j++) {
                var name = names[j];
                var info = byProject[name];
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
};

pages.site = function (name: string): void {
    setTitle(name + " - Sites");
    renderSidebar("sites");
    apiFetch<ProjectData>("/projects/" + name).then(function (d) {
        var p = d.project;
        var bu = d.base_url || "";
        var sitesBase = (d.services || {}).sites || "";
        var sites = d.sites || [];

        var html = '<h1><a href="#/sites">Sites</a> / ' + h(p.name) + "</h1>";

        html += '<div class="card"><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
        if (sites.length === 0) {
            html += '<tr><td colspan="6" class="empty">No branches deployed</td></tr>';
        } else {
            for (var i = 0; i < sites.length; i++) {
                var s = sites[i]!;
                html += "<tr><td><code>" + h(s.branch) + "</code></td>";
                html += "<td>" + s.file_count + "</td>";
                html += "<td>" + h(humanSize(s.size)) + "</td>";
                html += "<td>" + (s.git_commit ? '<code class="commit">' + h(s.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + h(formatTime(s.updated_at)) + '">' + h(timeAgo(s.updated_at)) + "</td>";
                html += '<td><a href="' + h(siteBranchURL(sitesBase, p.name, s.branch)) + '" target="_blank">Open</a></td></tr>';
            }
        }
        html += "</tbody></table></div>";

        html += '<div class="card"><h2>Deploy to ' + h(p.name) + "</h2>";
        html += codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project " + p.name + " \\\n  --branch {branch} \\\n  --dir ./dist");
        html += codeBlock("Delete a branch", 'curl -X DELETE \\\n  -H "Authorization: Bearer $TOKEN" \\\n  ' + bu + "/sites/" + p.name + "/branch/{branch}");
        html += "</div>";

        document.getElementById("content")!.innerHTML = html;
    });
};

pages.oidc = function (): void {
    setTitle("OIDC Policies");
    renderSidebar("oidc");
    apiFetch<OIDCPolicy[]>("/oidc").then(function (policies) {
        var html = '<h1>OIDC Policies</h1><div class="card"><table class="data-table"><thead><tr><th>Issuer</th><th>Subject Pattern</th><th>Audience</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th></tr></thead><tbody>';
        if (policies.length === 0) {
            html += '<tr><td colspan="7" class="empty">No OIDC policies configured</td></tr>';
        } else {
            for (var i = 0; i < policies.length; i++) {
                var p = policies[i];
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
};

pages.artifacts = function (): void {
    setTitle("Artifacts");
    renderSidebar("dashboard");
    apiFetch<AllArtifact[]>("/artifacts").then(function (artifacts) {
        var html = '<h1>All Artifacts</h1><div class="card"><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Platform</th><th>Kind</th><th>Filename</th><th>Size</th><th>Downloads</th><th>Created</th></tr></thead><tbody>';
        if (artifacts.length === 0) {
            html += '<tr><td colspan="8" class="empty">No artifacts yet</td></tr>';
        } else {
            for (var i = 0; i < artifacts.length; i++) {
                var a = artifacts[i];
                html += "<tr><td><a href='#/projects/" + h(a.project_name) + "'>" + h(a.project_name) + "</a></td>";
                html += "<td><a href='#/projects/" + h(a.project_name) + "/releases/" + h(a.version) + "'><code>" + h(a.version) + "</code></a></td>";
                html += "<td>" + platformBadge(a.platforms, a.exe_format, a.os, a.arch) + "</td>";
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
};

pages.storage = function (): void {
    setTitle("Storage");
    renderSidebar("dashboard");
    apiFetch<StorageData>("/storage").then(function (d) {
        var projects = d.projects || [];
        var html = '<h1>Storage Usage</h1><div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.total_bytes || 0)) + '</div><div class="stat-label">Artifact Storage</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.logical_bytes || 0)) + '</div><div class="stat-label">Logical Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.physical_bytes || 0)) + '</div><div class="stat-label">Physical Size (dedup)</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_bytes || 0)) + '</div><div class="stat-label">Blobs on Disk</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.reclaimable_bytes || 0)) + '</div><div class="stat-label">Reclaimable (est.)</div></div>';
        if (d.disk_total) {
            html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_used || 0)) + " / " + h(humanSize(d.disk_total || 0)) + '</div><div class="stat-label">Filesystem Usage</div></div>';
        }
        html += "</div>";

        var logical = d.logical_bytes || 0;
        var physical = d.physical_bytes || 0;
        var disk = d.disk_bytes || 0;
        var bdRow = function (op: string, name: string, bytes: number, cls?: string): string {
            return "<tr" + (cls ? ' class="' + cls + '"' : "") +
                '><td class="bd-op">' + op + '</td><td class="bd-name">' + name +
                '</td><td class="bd-val">' + h(humanSize(bytes)) + "</td></tr>";
        };
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
            for (var i = 0; i < projects.length; i++) {
                var p = projects[i]!;
                html += "<tr><td><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a></td>";
                html += "<td>" + p.release_count + "</td>";
                html += "<td>" + p.artifact_count + "</td>";
                html += "<td>" + h(humanSize(p.total_bytes)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content")!.innerHTML = html;
    });
};

pages.retention = function (): void {
    setTitle("Retention");
    renderSidebar("retention");
    apiFetch<RetentionData>("/retention").then(renderRetention);
};

const renderRetention = function (d: RetentionData): void {
    var p = d.preview || {};
    var rels = p.releases || [];

    var sweeper;
    if (!d.sweeper_enabled) {
        sweeper = badge("neutral", "Background sweeper: off");
    } else if (d.sweeper_enforce) {
        sweeper = badge("danger", "Background sweeper: ON — deleting automatically");
    } else {
        sweeper = badge("warning", "Background sweeper: ON — report-only");
    }

    var html = "<h1>Retention &amp; Garbage Collection</h1>";
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
        for (var i = 0; i < rels.length; i++) {
            var r = rels[i];
            var proj = r.project_name ? "<a href='#/projects/" + h(r.project_name) + "'>" + h(r.project_name) + "</a>" : h("project " + r.project_id);
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

    var form = document.getElementById("retention-form");
    if (form) {
        form.addEventListener("submit", function (e) {
            e.preventDefault();
            var keepN = parseInt((document.getElementById("ret-keepn") as HTMLInputElement | HTMLSelectElement).value, 10);
            var recency = parseInt((document.getElementById("ret-recency") as HTMLInputElement | HTMLSelectElement).value, 10);
            fetch("/api/retention", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ keep_n: keepN, recency_hours: recency })
            }).then(function (res) {
                if (!res.ok) return res.text().then(function (t) { alert("Error: " + t); });
                return res.json().then(renderRetention);
            }).catch(function () { alert("Could not save policy (preview/demo mode has no backend)."); });
        });
    }
};

const runRetention = function (): void {
    if (!confirm("Permanently delete the releases shown in the preview and reclaim their storage? This cannot be undone.")) return;
    fetch("/api/retention/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enforce: true })
    }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Error: " + t); });
        return res.json().then(function (rep) {
            alert("Garbage collection complete: evicted " + (rep.release_count || 0) + " releases, freed " +
                humanSize(rep.reclaimable_bytes || 0) + " across " + (rep.blobs || 0) + " blobs.");
            pages.retention();
        });
    }).catch(function () { alert("Could not run GC (preview/demo mode has no backend)."); });
};

// --- Go module proxy ---

const recheckGoproxy = function (): void {
    fetch("/api/goproxy/recheck", { method: "POST" }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Error: " + t); });
        return res.json().then(function () { pages.goproxy(); });
    }).catch(function () { alert("Could not re-check (preview/demo mode has no backend)."); });
};

pages.goproxy = function (): void {
    setTitle("Go Proxy");
    renderSidebar("goproxy");
    apiFetch<GoproxyData>("/goproxy").then(function (d) {
        if (!d.enabled || !d.state) {
            document.getElementById("content")!.innerHTML =
                '<h1>Go Proxy</h1><div class="card"><p class="empty">The Go module proxy is not running in this server.</p></div>';
            return;
        }
        var st = d.state;
        var hl = st.health;
        var html = '<h1>Go Proxy</h1>';

        // Health first, and loud. A proxy with no credential serves every public
        // module and no private one, so "it is up" is not the question worth
        // answering at the top of this page.
        var cls = hl.healthy ? (hl.reason ? "warn" : "ok") : "bad";
        var label = hl.healthy ? (hl.reason ? "Ready, but unproven" : "Ready") : "NOT serving private modules";
        html += '<div class="card goproxy-health goproxy-' + cls + '">';
        html += "<h2>" + h(label) + "</h2>";
        if (hl.reason) html += '<p class="section-desc">' + h(hl.reason) + "</p>";
        if (hl.probe_error) {
            html += "<p><strong>" + h(hl.probe_error_kind || "error") + "</strong></p>";
            html += "<pre class='goproxy-error'>" + h(hl.probe_error) + "</pre>";
        }
        html += '<table class="data-table"><tbody>';
        html += "<tr><td class='info-label'>Credential</td><td>" + h(hl.credential_kind) +
            (hl.credential_configured ? "" : " <strong>(none configured)</strong>") + "</td></tr>";
        html += "<tr><td class='info-label'>Private prefixes</td><td>" +
            h((hl.private_prefixes || []).join(", ") || "(none)") + "</td></tr>";
        html += "<tr><td class='info-label'>Upstream mirror</td><td>" +
            (hl.upstream
                ? h(hl.upstream)
                : "<em>none &mdash; modules outside the prefixes above are 404'd so <code>GOPROXY=&hellip;,direct</code> fetches them from their origin</em>") +
            "</td></tr>";
        html += "<tr><td class='info-label'>Readiness module</td><td>" +
            (hl.readiness_module
                ? h(hl.readiness_module) + (hl.probe_version ? " &rarr; " + h(hl.probe_version) : "")
                : "<em>not configured &mdash; the credential's ACCESS is unproven</em>") + "</td></tr>";
        html += "<tr><td class='info-label'>Checked</td><td>" + h(timeAgo(hl.checked_at)) + "</td></tr>";
        html += "</tbody></table>";
        html += '<p><button class="btn" onclick="App.recheckGoproxy()">Re-check now</button></p>';
        html += "</div>";

        var c = st.cache, t = st.traffic;
        html += '<div class="card"><h2>Cache</h2><div class="stat-grid">';
        html += statTile(c.modules, "Modules");
        html += statTile(c.versions, "Versions");
        html += statTile(c.zips, "Zips stored");
        html += statTile(humanSize(c.bytes || 0), "Cached bytes");
        html += statTile(c.failing_modules, "Failing modules");
        html += "</div></div>";

        html += '<div class="card"><h2>Traffic</h2><p class="section-desc">Counters since this process started; the cache figures above survive a restart.</p><div class="stat-grid">';
        html += statTile(t.cache_hits, "Cache hits");
        html += statTile(t.cache_misses, "Misses");
        html += statTile(t.fetches, "Upstream fetches");
        html += statTile(humanSize(t.bytes_sent || 0), "Bytes served");
        html += "</div>";
        var errs = t.errors || {};
        var kinds = Object.keys(errs);
        if (kinds.length > 0) {
            html += '<table class="data-table"><thead><tr><th>Failure</th><th>Count</th></tr></thead><tbody>';
            for (var e = 0; e < kinds.length; e++) {
                html += "<tr><td>" + h(kinds[e]!) + "</td><td>" + h(errs[kinds[e]!]!) + "</td></tr>";
            }
            html += "</tbody></table>";
        }
        html += "</div>";

        var mods = st.modules || [];
        html += '<div class="card"><h2>Modules</h2><table class="data-table"><thead><tr><th>Module</th><th>Source</th><th>Versions</th><th>Size</th><th>Last success</th><th>Last failure</th></tr></thead><tbody>';
        if (mods.length === 0) {
            html += '<tr><td colspan="6" class="empty">Nothing cached yet</td></tr>';
        } else {
            for (var i = 0; i < mods.length; i++) {
                var m = mods[i]!;
                html += "<tr" + (m.last_error_kind ? ' class="goproxy-row-bad"' : "") + ">";
                html += "<td><code>" + h(m.path) + "</code></td>";
                html += "<td>" + h(m.source) + (m.private ? " (private)" : "") + "</td>";
                html += "<td>" + h(m.versions) + "</td>";
                html += "<td>" + h(humanSize(m.bytes || 0)) + "</td>";
                html += "<td>" + h(m.last_success_at || "-") + "</td>";
                html += "<td>" + (m.last_error_kind
                    ? "<strong>" + h(m.last_error_kind) + "</strong><br><small>" + h(m.last_error) + "</small>"
                    : "-") + "</td>";
                html += "</tr>";
            }
        }
        html += "</tbody></table></div>";

        var recent = st.recent || [];
        html += '<div class="card"><h2>Recent requests</h2><table class="data-table"><thead><tr><th>When</th><th>Module</th><th>Version</th><th>Endpoint</th><th>Outcome</th><th>Status</th><th>Took</th></tr></thead><tbody>';
        if (recent.length === 0) {
            html += '<tr><td colspan="7" class="empty">No requests yet</td></tr>';
        } else {
            for (var j = 0; j < recent.length; j++) {
                var ev = recent[j]!;
                html += "<tr" + (ev.outcome === "error" ? ' class="goproxy-row-bad"' : "") + ">";
                html += "<td>" + h(timeAgo(ev.at)) + "</td>";
                html += "<td><code>" + h(ev.module) + "</code></td>";
                html += "<td>" + h(ev.version || "-") + "</td>";
                html += "<td>" + h(ev.endpoint) + "</td>";
                html += "<td>" + h(ev.outcome) + (ev.detail ? " (" + h(ev.detail) + ")" : "") + "</td>";
                html += "<td>" + h(ev.status) + "</td>";
                html += "<td>" + h(ev.duration) + "</td>";
                html += "</tr>";
            }
        }
        html += "</tbody></table></div>";

        document.getElementById("content")!.innerHTML = html;
    });
};

// --- Router ---

const route = function (): void {
    var hash = window.location.hash.replace(/^#\/?/, "") || "";

    var releaseM = hash.match(/^projects\/(.+)\/releases\/([^\/]+)$/);
    if (releaseM) { pages.release(releaseM[1], releaseM[2]); return; }

    var projectM = hash.match(/^projects\/(.+)$/);
    if (projectM) { pages.project(projectM[1]); return; }

    var siteM = hash.match(/^sites\/(.+)$/);
    if (siteM) { pages.site(siteM[1]); return; }

    var first = hash.split("/")[0];
    if (first === "projects") { pages.projects(); }
    else if (first === "registries") { pages.registries(); }
    else if (first === "sites") { pages.sites(); }
    else if (first === "tokens") { pages.tokens(); }
    else if (first === "oidc") { pages.oidc(); }
    else if (first === "artifacts") { pages.artifacts(); }
    else if (first === "storage") { pages.storage(); }
    else if (first === "retention") { pages.retention(); }
    else if (first === "goproxy") { pages.goproxy(); }
    else { pages.dashboard(); }
};

// --- Demo data ---

let demoServices = {
    dl: "https://dl.builds.example.com",
    apt: "https://apt.builds.example.com",
    brew: "https://brew.builds.example.com",
    npm: "https://npm.builds.example.com",
    oci: "https://oci.builds.example.com",
    sites: "https://sites.builds.example.com",
    static: "https://static.builds.example.com"
};

const demoData: Record<string, unknown> = {
    "/sidebar": { build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" }, build_age: "", cpu_percent: "0.0%", disk_used: "0 B", disk_total: "0 B" },
    "/dashboard": {
        stats: { project_count: 2, release_count: 5, artifact_count: 12, total_storage_bytes: 52428800, token_count: 3, site_count: 3 },
        recent: [
            { project_name: "myapp", version: "3", git_branch: "main", published: true, created_at: new Date(Date.now() - 3600000).toISOString() },
            { project_name: "cli-tool", version: "1.2.0", git_branch: "release", published: true, created_at: new Date(Date.now() - 86400000).toISOString() }
        ],
        config: { base_url: "https://builds.example.com", listen_addr: ":8080", admin_listen_addr: ":9090", data_dir: "./data", oidc_issuers: ["https://token.actions.githubusercontent.com"], oidc_orgs: ["myorg"], oidc_events: ["push"] },
        services: demoServices,
        build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" },
        uptime: "0m 0s", cpu_percent: "0.0%", cpu_total: "0m 0s"
    },
    "/projects": [
        { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, release_count: 3, artifact_count: 8, created_at: new Date(Date.now() - 864e5 * 30).toISOString() },
        { id: 2, name: "cli-tool", description: "CLI utility", versioning: "semver", is_private: true, release_count: 2, artifact_count: 4, created_at: new Date(Date.now() - 864e5 * 10).toISOString() }
    ],
    "/projects/myapp": {
        project: { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, created_at: new Date(Date.now() - 864e5 * 30).toISOString(), updated_at: new Date(Date.now() - 3600000).toISOString() },
        releases: [{ version: "3", git_branch: "main", git_commit: "abc123", published: true, artifact_count: 4, published_at: new Date(Date.now() - 3600000).toISOString(), created_at: new Date(Date.now() - 3600000).toISOString() }],
        sites: [{ branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }],
        base_url: "https://builds.example.com",
        services: demoServices
    },
    "/registries": { base_url: "https://builds.example.com", services: demoServices, projects: [{ name: "myapp", is_private: false }, { name: "cli-tool", is_private: true }] },
    "/sites": { sites: [{ project_name: "myapp", branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { project_name: "myapp", branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }, { project_name: "cli-tool", branch: "main", file_count: 8, size: 23000, git_commit: "fff000111222", updated_at: new Date(Date.now() - 86400000).toISOString() }], base_url: "https://builds.example.com", services: demoServices },
    // The demo deliberately shows an UNHEALTHY proxy: the failure mode this page
    // exists for (a credential that cannot read private modules while public ones
    // keep working) is the one worth showing off in a preview.
    "/goproxy": {
        enabled: true,
        state: {
            health: {
                healthy: false,
                reason: "the readiness module github.com/myorg/internal-lib did not resolve",
                credential_configured: true,
                credential_kind: "token",
                private_prefixes: ["github.com/myorg"],
                upstream: "",
                readiness_module: "github.com/myorg/internal-lib",
                probed: true,
                probe_error_kind: "unauthorized",
                probe_error: "unauthorized: module github.com/myorg/internal-lib: github responded 404: the proxy's credential is presented; if the repository does exist, that credential is not authorized for it",
                checked_at: new Date(Date.now() - 120000).toISOString()
            },
            cache: { modules: 3, versions: 12, zips: 9, bytes: 4_200_000, failing_modules: 1 },
            traffic: { since_start: true, cache_hits: 148, cache_misses: 12, fetches: 12, bytes_sent: 31_000_000, errors: { unauthorized: 4, not_found: 1 } },
            modules: [
                { path: "github.com/myorg/internal-lib", source: "github", private: true, versions: 0, bytes: 0, last_error_kind: "unauthorized", last_error: "github responded 404 for a repository the credential is not authorized for", last_error_at: "2026-01-01 00:00:00Z", last_success_at: "", last_fetched_at: "" },
                { path: "github.com/myorg/tools", source: "github", private: true, versions: 4, bytes: 1_100_000, last_error_kind: "", last_error: "", last_success_at: "2026-01-01 00:00:00Z", last_fetched_at: "2026-01-01 00:00:00Z" },
                { path: "github.com/myorg/agentic-loop/go", source: "github", private: true, versions: 8, bytes: 3_100_000, last_error_kind: "", last_error: "", last_success_at: "2026-01-01 00:00:00Z", last_fetched_at: "2026-01-01 00:00:00Z" }
            ],
            recent: [
                { at: new Date(Date.now() - 30000).toISOString(), module: "github.com/myorg/internal-lib", version: "", endpoint: "latest", source: "github", outcome: "error", status: 403, detail: "unauthorized", duration: "212ms" },
                { at: new Date(Date.now() - 90000).toISOString(), module: "github.com/myorg/agentic-loop/go", version: "v0.40.0", endpoint: "zip", source: "github", outcome: "hit", status: 200, detail: "", duration: "4ms" },
                { at: new Date(Date.now() - 150000).toISOString(), module: "github.com/myorg/tools", version: "v1.4.0", endpoint: "mod", source: "github", outcome: "fetch", status: 200, detail: "", duration: "684ms" }
            ]
        }
    },
    "/tokens": [{ id: 1, name: "deploy", token_prefix: "bh_abc", is_global: false, project_id: 1, project_name: "myapp", scopes: "read,write", is_expired: false, created_at: new Date(Date.now() - 864e5 * 7).toISOString(), last_used_at: new Date(Date.now() - 3600000).toISOString() }],
    "/oidc": [{ issuer: "https://token.actions.githubusercontent.com", subject_pattern: "repo:myorg/myapp:*", audience: "", project_name: "myapp", scopes: "read,write", created_at: new Date(Date.now() - 864e5 * 14).toISOString() }],
    "/artifacts": [
        { id: 1, os: "linux", arch: "amd64", kind: "binary", size: 15728640, filename: "myapp", created_at: new Date(Date.now() - 3600000).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 42 },
        { id: 2, os: "darwin", arch: "arm64", kind: "binary", size: 14680064, filename: "myapp", created_at: new Date(Date.now() - 3600000).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 18 },
        { id: 3, os: "linux", arch: "amd64", kind: "binary", size: 10485760, filename: "cli-tool", created_at: new Date(Date.now() - 86400000).toISOString(), version: "1.2.0", git_branch: "release", project_name: "cli-tool", download_count: 7 }
    ],
    "/storage": {
        projects: [
            { id: 1, name: "myapp", total_bytes: 45000000, artifact_count: 8, release_count: 3 },
            { id: 2, name: "cli-tool", total_bytes: 7428800, artifact_count: 4, release_count: 2 }
        ],
        total_bytes: 52428800, logical_bytes: 58000000, physical_bytes: 48000000, disk_bytes: 50000000,
        disk_used: 120000000, disk_total: 500000000
    },
    "/retention": {
        keep_n: 10, recency_hours: 24, sweeper_enabled: false, sweeper_enforce: false,
        preview: {
            enforced: false, release_count: 3, keep_n_count: 2, abandoned_count: 1,
            blobs: 4, blobs_retained: 1, reclaimable_bytes: 18874368,
            releases: [
                { project_name: "myapp", project_id: 1, branch: "main", version: "7", reason: "keep-n" },
                { project_name: "myapp", project_id: 1, branch: "main", version: "8", reason: "keep-n" },
                { project_name: "cli-tool", project_id: 2, branch: "feature-x", version: "3", reason: "abandoned" }
            ]
        }
    }
};

// --- Init ---

document.addEventListener("DOMContentLoaded", function () {
    if (window.location.pathname !== "/") demo = true;
    apiFetch<SidebarData>("/sidebar").then(function (data) {
        sidebarCache = data;
        route();
    });
});

window.addEventListener("hashchange", function () {
    route();
});

// Exported == reachable as App.x from the inline onclick handlers above.
export { copyTempLink, copyText, deleteToken, downloadArtifact, editToken, pages, recheckGoproxy, reloadTokens, runRetention, saveToken };
