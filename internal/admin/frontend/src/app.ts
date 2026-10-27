// The admin dashboard SPA.

import { Html, type El } from "./html.ts";

import type {
    SiteFilesData,
    AllArtifact,
    DashboardData,
    DownloadLink,
    DuplicatesData,
    GitHubCredentialData,
    GoproxyData,
    MergePlan,
    OIDCPolicy,
    Pages,
    Platform,
    ProjectData,
    ProjectSummary,
    RegistriesData,
    ReleaseData,
    DeletedBranchesReport,
    RetentionData,
    RetentionInventory,
    RunFile,
    RunFilesData,
    RunFilesRun,
    CacheData,
    CacheEntryRow,
    MirrorBlobRow,
    MirrorData,
    MirrorTagRow,
    ServiceURLs,
    SettingRow,
    SettingsData,
    SidebarData,
    SitesData,
    StorageData,
    TokenInfo,
    TreeRow,
    VisibilityFolder,
    VisibilityMode,
} from "./types.ts";
import type { TimelineHit, TimelineInterval, TimelineLane, TimelineViewElement } from "https://sites.pazer.build/js-snippets/@library/ui/timeline-view.js";
import type { DataTableElement } from "https://sites.pazer.build/js-snippets/@library/ui/data-table.js";
import type { BarChartElement } from "https://sites.pazer.build/js-snippets/@library/ui/bar-chart.js";


let demo = false;
let sidebarCache: SidebarData | null = null;

const h = function (s: string | number | null | undefined): string {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
};

// siteBranchURL links to a site's deployment. "@" is the read grammar; the
// "/branch/" spelling only redirects here.
const siteBranchURL = function (sitesBase: string, project: string, branch: string): string {
    return sitesBase + "/" + project + "/@" + branch + "/";
};

// siteFilesHash is the dashboard route of a single site branch's file list.
const siteFilesHash = function (project: string, branch: string): string {
    return "#/sites/" + project + "/-/files/" + encodeURIComponent(branch);
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

// The clock is the reader's own, so it carries no zone name.
const formatTime = function (s: string | null | undefined): string {
    if (!s) return "-";
    var d = new Date(s);
    if (isNaN(d.getTime())) return "-";
    var pad = function (n: number): string { return n < 10 ? "0" + n : "" + n; };
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
        " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
};

// The demo dataset must answer every path the demo's own pages ask for.
const demoFetch = function <T>(path: string): T {
    if (!Object.prototype.hasOwnProperty.call(demoData, path)) {
        throw new Error("no demo data for " + path);
    }
    return demoData[path] as T;
};

const apiFetch = function <T>(path: string): Promise<T> {
    if (demo) return Promise.resolve().then(function () { return demoFetch<T>(path); });
    return fetch("/api" + path).then(function (r) {
        if (!r.ok) throw new Error(String(r.status));
        return r.json();
    }).catch(function () {
        demo = true;
        return demoFetch<T>(path);
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
    { id: "run-files", href: "#/run-files", label: "Run Files", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M3 4a1 1 0 011-1h12a1 1 0 011 1v2a1 1 0 01-1 1H4a1 1 0 01-1-1V4zm0 6a1 1 0 011-1h12a1 1 0 011 1v2a1 1 0 01-1 1H4a1 1 0 01-1-1v-2zm1 5a1 1 0 100 2h7a1 1 0 100-2H4z"/></svg>' },
    { id: "cache", href: "#/cache", label: "CI Cache", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M3 5c0-1.1 3.1-2 7-2s7 .9 7 2v10c0 1.1-3.1 2-7 2s-7-.9-7-2V5zm7 2c3.9 0 7-.9 7-2s-3.1-2-7-2-7 .9-7 2 3.1 2 7 2z"/></svg>' },
    { id: "github", href: "#/github", label: "GitHub", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M18 8a6 6 0 01-7.743 5.743L10 14l-1 1-1 1H6v2H2v-4l4.257-4.257A6 6 0 1118 8zm-6-4a1 1 0 100 2 2 2 0 012 2 1 1 0 102 0 4 4 0 00-4-4z" clip-rule="evenodd"/></svg>' },
    { id: "settings", href: "#/settings", label: "Settings", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M11.49 3.17c-.38-1.56-2.6-1.56-2.98 0a1.53 1.53 0 01-2.29.95c-1.37-.84-2.94.73-2.1 2.1.54.89.07 2.04-.95 2.29-1.56.38-1.56 2.6 0 2.98a1.53 1.53 0 01.95 2.29c-.84 1.37.73 2.94 2.1 2.1a1.53 1.53 0 012.29.95c.38 1.56 2.6 1.56 2.98 0a1.53 1.53 0 012.29-.95c1.37.84 2.94-.73 2.1-2.1a1.53 1.53 0 01.95-2.29c1.56-.38 1.56-2.6 0-2.98a1.53 1.53 0 01-.95-2.29c.84-1.37-.73-2.94-2.1-2.1a1.53 1.53 0 01-2.29-.95zM10 13a3 3 0 100-6 3 3 0 000 6z" clip-rule="evenodd"/></svg>' },
    { id: "mirror", href: "#/mirror", label: "Registry Mirror", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M3 6a1 1 0 011-1h12a1 1 0 011 1v2H3V6zm0 4h14v4a1 1 0 01-1 1H4a1 1 0 01-1-1v-4zm3 2a1 1 0 100 2h2a1 1 0 100-2H6z"/></svg>' },
    { id: "oidc", href: "#/oidc", label: "OIDC Policies", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>' },
    { id: "retention", href: "#/retention", label: "Retention", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z" clip-rule="evenodd"/></svg>' },
    { id: "duplicates", href: "#/duplicates", label: "Duplicates", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M7 3a2 2 0 00-2 2v8a2 2 0 002 2h6a2 2 0 002-2V5a2 2 0 00-2-2H7z"/><path d="M3 7a2 2 0 012-2v10h8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/></svg>' }
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
// platformBadge renders an artifact's whole platform set as A single badge. Consider a file
// covering several platforms. That file is a single artifact with a single download link, so
// listing it a single time with "APE: linux/amd64, darwin/arm64" is the honest row.
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

// A Homebrew formula name cannot contain '/', so a slash-namespaced project
// folds its namespace to '-' in the tap.
const brewFormulaName = function (project: string): string {
    return project.replace(/\//g, "-");
};

const brewInstall = function (brewBase: string, project: string): string {
    return "brew trust " + brewBase + "/tap.git\nbrew tap pazer/build " + brewBase + "/tap.git\nbrew install pazer/build/" + brewFormulaName(project);
};

// Install commands, shared by the project page (latest) and the release page
// (one version).

// A Debian package name folds '/' and '_' to '-' (repackage.DebPackageName),
// so apt and dpkg agree on a slash-namespaced project.
const debPackageName = function (project: string): string {
    return project.replace(/[/_]/g, "-");
};

const host = function (url: string): string {
    return url.replace(/^https?:\/\//, "");
};

// The server-generated install.sh saves the armored key, writes the signed-by
// source, records the token for a private repo, and refreshes the index.
const aptOneLiner = function (aptBase: string, priv: boolean): string {
    return priv
        ? 'curl -fsSL -H "Authorization: Bearer $TOKEN" ' + aptBase + "/install.sh \\\n  | sudo BUILDHOST_TOKEN=$TOKEN sh"
        : "curl -fsSL " + aptBase + "/install.sh | sudo sh";
};

const aptManual = function (svc: ServiceURLs, project: string, priv: boolean): string {
    var aptBase = (svc.apt || "") + "/" + project;
    var pkg = debPackageName(project);
    return priv
        ? 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + pkg + '.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + host(svc.apt || "") + ' login token password $TOKEN\nmachine ' + host(svc.static || "") + ' login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install ' + pkg
        : 'sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ' + aptBase + '/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + aptBase + ' stable main" \\\n  | sudo tee /etc/apt/sources.list.d/' + pkg + '.list\nsudo apt update && sudo apt install ' + pkg;
};

// npm and the OCI tag list both carry every published release, so both pin a
// version. A deb and an npm package drop a leading "v" from the version
// (repackage.DebPackageName's siblings do the same), and an OCI tag does not.
const npmInstall = function (svc: ServiceURLs, project: string, version: string, priv: boolean): string {
    var registry = svc.npm || "";
    var spec = "@buildhost/" + project + (version ? "@" + version.replace(/^v/, "") : "");
    var cmd = "npm install " + spec + " --registry " + registry;
    return priv ? "npm config set //" + host(registry) + "/:_authToken $TOKEN\n" + cmd : cmd;
};

const dockerPull = function (svc: ServiceURLs, project: string, version: string, priv: boolean): string {
    var ociHost = host(svc.oci || "");
    var cmd = "docker pull " + ociHost + "/" + project + ":" + (version || "latest");
    return priv ? "echo $TOKEN | docker login " + ociHost + " -u token --password-stdin\n" + cmd : cmd;
};

// A private project's dl link redirects to the static host, and curl only
// re-sends credentials across that hop with --location-trusted. The token is
// the HTTP Basic password.
const curlDownload = function (dlBase: string, version: string, priv: boolean): string {
    var url = dlBase + (version ? "?v=" + version + "&" : "?") + "os=linux&arch=amd64";
    return priv
        ? 'curl -fsSL --location-trusted -u "token:$TOKEN" -O \\\n  "' + url + '"'
        : 'curl -fsSL -O "' + url + '"';
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
    var walk = function (node: TreeNode, depth: number, prefix: string): void {
        var names = Object.keys(node.children).sort();
        for (var i = 0; i < names.length; i++) {
            var child = node.children[names[i]!]!;
            var path = prefix ? prefix + "/" + child.name : String(child.name);
            if (Object.keys(child.children).length > 0) {
                out.push({ kind: "folder", depth: depth, name: child.name, path: path });
            }
            if (child.project) {
                out.push({ kind: "project", depth: depth, project: child.project });
            }
            walk(child, depth + 1, path);
        }
    };
    walk(root, 0, "");
    return out;
};

const projectLabel = function (name: string): string {
    var s = String(name || "");
    var i = s.lastIndexOf("/");
    return i >= 0 ? s.substring(i + 1) : s;
};

const visibilityBadge = function (priv: boolean): string {
    return priv ? badge("warning", "Private") : badge("success", "Public");
};

// visibilitySelect renders the three-way override control. inherited is what
// "auto" resolves to here; attrs are data-* attributes that tell setVisibility
// which level to change.
const visibilitySelect = function (mode: VisibilityMode, inherited: string, attrs: Record<string, string>): string {
    var opts: [VisibilityMode, string][] = [
        ["auto", "Auto (" + inherited + ")"],
        ["public", "Force public"],
        ["private", "Force private"],
    ];
    var html = '<select class="visibility-select" onchange="App.setVisibility(this)"';
    for (var k in attrs) html += " data-" + k + '="' + h(attrs[k]!) + '"';
    html += ">";
    for (var i = 0; i < opts.length; i++) {
        html += '<option value="' + opts[i]![0] + '"' + (opts[i]![0] === mode ? " selected" : "") + ">" + h(opts[i]![1]) + "</option>";
    }
    return html + "</select>";
};

const setVisibility = function (sel: HTMLSelectElement): void {
    var d = sel.dataset;
    var mode = sel.value;
    var url: string, body: Record<string, string> = { mode: mode }, what: string;
    var reload = function (): void { route(); };
    if (d.level === "folder") {
        url = "/api/visibility-folders";
        body.prefix = d.prefix || "";
        what = "folder " + d.prefix + "/";
    } else if (d.level === "project") {
        url = "/api/projects/" + encodeURIComponent(d.project || "") + "/visibility";
        what = "project " + d.project;
    } else if (d.level === "release") {
        url = "/api/projects/" + encodeURIComponent(d.project || "") + "/releases/" + encodeURIComponent(d.version || "") + "/visibility";
        what = d.project + " " + d.version;
    } else if (d.level === "site") {
        url = "/api/projects/" + encodeURIComponent(d.project || "") + "/site-visibility";
        body.branch = d.branch || "";
        what = d.project + " site @" + d.branch;
    } else {
        return;
    }
    var label = mode === "auto" ? "auto (follow the level above)" : "forced " + mode;
    if (!confirm("Set " + what + " to " + label + "?")) { reload(); return; }
    fetch(url, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
    }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Error: " + t); reload(); });
        reload();
    }).catch(function () { alert("Could not change visibility (preview/demo mode has no backend)."); reload(); });
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
    Promise.all([
        apiFetch<ProjectSummary[]>("/projects"),
        apiFetch<VisibilityFolder[]>("/visibility-folders"),
    ]).then(function (res) {
        var projects = res[0];
        var folderModes: Record<string, VisibilityMode> = {};
        for (var fi = 0; fi < (res[1] || []).length; fi++) folderModes[res[1][fi]!.prefix] = res[1][fi]!.mode;
        // inheritedFolder names the nearest rule ABOVE path, which is what "auto" means for it.
        var inheritedFolder = function (path: string): string {
            for (var i = path.lastIndexOf("/"); i > 0; i = path.lastIndexOf("/", i - 1)) {
                var m = folderModes[path.substring(0, i)];
                if (m) return m + " from " + path.substring(0, i) + "/";
            }
            return "each project's repo";
        };
        var html = '<h1>Projects</h1>';
        html += '<p class="section-desc">Visibility: a build override beats its project, a project override beats its folder, and a folder rule beats the GitHub repo that published the project. <b>Auto</b> follows the level above.</p>';
        html += '<div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Description</th><th>Versioning</th><th>Visibility</th><th>Releases</th><th>Artifacts</th><th>Created</th></tr></thead><tbody>';
        if (projects.length === 0) {
            html += '<tr><td colspan="7" class="empty">No projects yet</td></tr>';
        } else {
            var rows = projectTreeRows(projects);
            for (var i = 0; i < rows.length; i++) {
                var row = rows[i];
                if (row.kind === "folder") {
                    var path = row.path || String(row.name);
                    html += '<tr class="project-folder-row"><td colspan="3" class="project-folder-cell project-depth-' + row.depth + '"><span class="project-folder-icon"></span>' + h(row.name) + "</td>";
                    html += '<td colspan="4">' + visibilitySelect(folderModes[path] || "auto", inheritedFolder(path), { level: "folder", prefix: path }) + "</td></tr>";
                    continue;
                }
                var p = row.project!;
                var v = p.visibility;
                html += "<tr class='project-tree-row'>";
                html += projectNameCell(p, row.depth);
                html += '<td class="truncate">' + h(p.description) + "</td>";
                html += "<td>" + badge("neutral", p.versioning) + "</td>";
                html += "<td>" + visibilityBadge(p.is_private);
                if (v && v.mode !== "auto") html += " " + badge("neutral", "forced");
                else if (v && v.folder_mode !== "auto") html += " " + badge("neutral", "folder " + v.folder_prefix + "/");
                html += "</td>";
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
        var versioningOpts = "";
        for (var vo of ["auto", "semver"]) {
            versioningOpts += '<option value="' + vo + '"' + (vo === p.versioning ? " selected" : "") + ">" + vo + "</option>";
        }
        html += "<tr><td class='info-label'>Versioning</td><td>" +
            '<select id="versioning-select" onchange="App.setVersioning(\'' + h(p.name) + '\', this.value)">' + versioningOpts + "</select>" +
            "</td></tr>";
        var pv = d.visibility;
        var autoSource = pv && pv.folder_mode !== "auto"
            ? pv.folder_mode + " from folder " + pv.folder_prefix + "/"
            : (pv && pv.auto_private ? "private" : "public") + (p.github_repo ? " from " + p.github_repo : " as created");
        html += "<tr><td class='info-label'>Visibility</td><td>" + visibilityBadge(p.is_private) + " " +
            visibilitySelect(pv ? pv.mode : "auto", autoSource, { level: "project", project: p.name }) + "</td></tr>";
        html += '<tr><td class="info-label">Created</td><td title="' + h(formatTime(p.created_at)) + '">' + h(timeAgo(p.created_at)) + "</td></tr>";
        html += '<tr><td class="info-label">Updated</td><td title="' + h(formatTime(p.updated_at)) + '">' + h(timeAgo(p.updated_at)) + "</td></tr>";
        html += "</table></div>";

        // Download & Install (latest).
        var hasPublished = false;
        for (var ri = 0; ri < rels.length; ri++) { if (rels[ri].published) { hasPublished = true; break; } }
        if (hasPublished) {
            var dlBase = (svc.dl || "") + "/" + p.name;
            var aptBase = (svc.apt || "") + "/" + p.name;
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


            html += Html.div(
                Html.h2("Download & Install ", Html.span("— latest").cls("muted").style("font-weight:400")),
                Html.p("Always resolves to the newest published release. To pin a specific version, open a release below.").cls("section-desc"),
                Html.table(
                    Html.tr(
                        Html.td("Direct download").cls("info-label"),
                        Html.td(Html.raw(urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch="))).cls("endpoint-cell")
                    ),
                    endpointRow("APT repository", aptBase, aptBase + "/dists/stable/Release", aptBase)
                ).cls("info-table"),
                Html.raw(codeBlock("Direct download (curl)", curlDownload(dlBase, "", priv))),
                Html.raw(codeBlock("APT (one-line install)", aptOneLiner(aptBase, priv))),
                Html.raw(codeBlock("APT (manual setup)", aptManual(svc, p.name, priv))),
                Html.raw(codeBlock("Homebrew", brewInstall(svc.brew || "", p.name))),
                Html.raw(codeBlock("npm", npmInstall(svc, p.name, "", priv))),
                Html.raw(codeBlock("Docker", dockerPull(svc, p.name, "", priv)))
            ).cls("card");
        }

        var projectVis = p.is_private ? "private" : "public";
        var releaseModes = d.release_modes || {};
        html += '<div class="card"><h2>Releases</h2><table class="data-table"><thead><tr><th>Version</th><th>Branch</th><th>Commit</th><th>Status</th><th>Visibility</th><th>Artifacts</th><th>Published</th><th>Created</th></tr></thead><tbody>';
        if (rels.length === 0) {
            html += '<tr><td colspan="8" class="empty">No releases yet</td></tr>';
        } else {
            for (var i = 0; i < rels.length; i++) {
                var r = rels[i];
                html += "<tr><td><a href='#/projects/" + h(p.name) + "/releases/" + h(r.version) + "'><code>" + h(r.version) + "</code></a></td>";
                html += "<td>" + (r.git_branch ? "<code>" + h(r.git_branch) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.git_commit ? '<code class="commit">' + h(r.git_commit) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.published ? badge("success", "Published") : badge("warning", "Draft")) + "</td>";
                html += "<td>" + visibilitySelect(releaseModes[r.version] || "auto", projectVis, { level: "release", project: p.name, version: r.version }) + "</td>";
                html += "<td>" + r.artifact_count + "</td>";
                html += "<td>" + h(formatTime(r.published_at)) + "</td>";
                html += '<td title="' + h(formatTime(r.created_at)) + '">' + h(timeAgo(r.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        var sites = d.sites || [];
        if (sites.length > 0) {
            var sitesBase = svc.sites || "";
            var siteModes = d.site_modes || {};
            html += '<div class="card"><h2>Sites</h2><table class="data-table"><thead><tr><th>Branch</th><th>Visibility</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
            for (var k = 0; k < sites.length; k++) {
                var si = sites[k];
                html += "<tr><td><code>" + h(si.branch) + "</code></td>";
                html += "<td>" + visibilitySelect(siteModes[si.branch] || "auto", si.is_public ? "public, published public" : projectVis, { level: "site", project: p.name, branch: si.branch }) + "</td>";
                html += "<td>" + si.file_count + "</td>";
                html += "<td>" + h(humanSize(si.size)) + "</td>";
                html += "<td>" + (si.git_commit ? '<code class="commit">' + h(si.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + h(formatTime(si.updated_at)) + '">' + h(timeAgo(si.updated_at)) + "</td>";
                html += '<td><a href="' + h(siteFilesHash(p.name, si.branch)) + '">Files</a> &middot; <a href="' + h(siteBranchURL(sitesBase, p.name, si.branch)) + '" target="_blank">Open</a></td></tr>';
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
        var relMode: VisibilityMode = d.release_mode || "auto";
        var priv = relMode === "auto" ? !!p.is_private : relMode === "private";
        var html = "<h1><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a> / " + h(r.version) + "</h1>";

        html += '<div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + (d.artifacts || []).length + '</div><div class="stat-label">Artifacts</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.total_size || 0)) + '</div><div class="stat-label">Total Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + (d.total_downloads || 0) + '</div><div class="stat-label">Downloads</div></div>';
        html += "</div>";

        html += '<div class="card"><h2>Release Info</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Version</td><td><code>" + h(r.version) + "</code></td></tr>";
        html += "<tr><td class='info-label'>Status</td><td>" + (r.published ? badge("success", "Published") : badge("warning", "Draft")) + "</td></tr>";
        html += "<tr><td class='info-label'>Visibility</td><td>" + visibilityBadge(priv) + " " +
            visibilitySelect(relMode, p.is_private ? "private" : "public", { level: "release", project: p.name, version: r.version }) +
            '<div class="muted">Covers direct downloads (dl, static), npm tarballs and a docker pull by tag. Package indexes (APT, Homebrew, npm metadata) follow the project. A forced-public build of a private project stays private to docker, because image layers are fetched by digest.</div></td></tr>';
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
                var dlQ = "?v=" + r.version + "&os=" + a.os + "&arch=" + a.arch;
                if (priv) {
                    // Each link mints a signed, single-artifact link on click, then downloads it.
                    html += dlMintLink(dlBase + dlQ, p.name, r.version, a.os, a.arch, "raw", false, "raw", "Download (mints a temporary signed link)");
                    if (a.debug_storage_key) html += " " + dlMintLink(dlBase + dlQ + "&debug=1", p.name, r.version, a.os, a.arch, "raw", true, "debug", "Debug symbols");
                    for (var j = 0; j < pkgs.length; j++) {
                        html += " " + dlMintLink(dlBase + dlQ + "&fmt=" + pkgs[j].format, p.name, r.version, a.os, a.arch, pkgs[j].format, false, pkgs[j].format, pkgs[j].filename + " (" + humanSize(pkgs[j].size) + ")");
                    }
                    html += ' <button type="button" class="dl-share" onclick="App.copyTempLink(this,\'' + h(p.name) + '\',\'' + h(r.version) + '\',\'' + h(a.os) + '\',\'' + h(a.arch) + '\',\'raw\')" title="Copy a temporary 1-hour shareable link">temp link</button>';
                } else {
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

        // A URL belongs here only where the URL IS the product: the direct download.
        html += '<div class="card"><h2>Download Endpoints</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Direct (latest)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>Direct (version)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?v=" + r.version + "&os={os}&arch={arch}", dlBase + "?v=" + r.version + "&os=", "&arch=") + "</td></tr>";
        if (r.git_branch) html += "<tr><td class='info-label'>Direct (branch)</td><td class='endpoint-cell'>" + urlTpl(dlBase + "?branch=" + r.git_branch + "&os={os}&arch={arch}", dlBase + "?branch=" + r.git_branch + "&os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>APT repository</td><td class='endpoint-cell'><a href='" + h(aptU + "/dists/stable/Release") + "' data-copy='" + h(aptU) + "'>" + h(aptU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "</table></div>";

        // npm metadata and image layers follow the project: a private project OR a private build needs a token.
        var relPriv = !!p.is_private || relMode === "private";
        html += '<div class="card"><h2>Install</h2>';
        html += codeBlock("Direct download (curl)", curlDownload(dlBase, r.version, priv));
        html += codeBlock("npm", npmInstall(svc, p.name, r.version, relPriv));
        html += codeBlock("Docker", dockerPull(svc, p.name, r.version, relPriv));
        html += codeBlock("APT (one-line install, always the latest release)", aptOneLiner(aptU, !!p.is_private));
        html += codeBlock("Homebrew (always the latest release)", brewInstall(svc.brew || "", p.name));
        html += "</div>";

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
        html += codeBlock("Setup (public project)", 'sudo install -d -m 0755 /etc/apt/keyrings\ncurl -fsSL ' + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{package}.list\nsudo apt update && sudo apt install {package}');
        html += codeBlock("Setup (private project)", 'sudo install -d -m 0755 /etc/apt/keyrings\n# the token is the HTTP Basic password (username is ignored)\ncurl -fsSL -u "token:$TOKEN" ' + apt + '/{project}/key.asc | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg\necho "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{package}.list\n# both apt (metadata) and static (the .deb download redirect) need the token\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf\nmachine ' + aptHost + ' login token password $TOKEN\nmachine ' + staticHost + ' login token password $TOKEN\nEOF\nsudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf\nsudo apt update && sudo apt install {package}');
        html += "</div>";

        html += '<div class="card"><h2>Homebrew Tap</h2><p class="section-desc">Homebrew formulas are served through a generated Git tap. Formula files auto-detect macOS and Linux artifacts. A formula name cannot contain <code>/</code>, so a slash-namespaced project folds it to <code>-</code> &mdash; e.g. <code>myrepo/server</code> installs as <code>myrepo-server</code>.</p>';
        html += '<table class="info-table"><tr><td class="info-label">Tap Git URL</td><td class="endpoint-cell"><code>' + h(brew + "/tap.git") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += '<tr><td class="info-label">Formula</td><td class="endpoint-cell"><code>' + h(brew + "/Formula/{formula}.rb") + "</code><copy-btn data-src='code'></copy-btn></td></tr></table>";
        html += codeBlock("Install", "brew trust " + brew + "/tap.git\nbrew tap pazer/build " + brew + "/tap.git\nbrew install pazer/build/{formula}");
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
            html += '<div class="card"><h2>Projects</h2><p class="section-desc">The command that installs each project, plus its direct-download URL.</p>';
            html += '<table class="data-table"><thead><tr><th>Project</th><th>Visibility</th><th>Direct Download</th><th>APT</th><th>Brew</th><th>npm</th></tr></thead><tbody>';
            for (var k = 0; k < projects.length; k++) {
                var pr = projects[k];
                var prDl = dl + "/" + pr.name;
                html += "<tr><td><a href='#/projects/" + h(pr.name) + "'>" + h(pr.name) + "</a></td>";
                html += "<td>" + (pr.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
                html += "<td class='endpoint-cell'><span class='url-tpl' data-tpl='" + h(prDl + "?os={os}&arch={arch}") + "'><code class='truncate'>" + h(prDl + "?os=") + "</code><select class='tpl-select tpl-select-sm' data-var='os'><option value='linux'>linux</option><option value='darwin'>darwin</option><option value='windows'>windows</option><option value='freebsd'>freebsd</option></select><code>&arch=</code><select class='tpl-select tpl-select-sm' data-var='arch'><option value='amd64'>amd64</option><option value='arm64'>arm64</option><option value='386'>386</option><option value='arm'>arm</option></select></span><copy-btn></copy-btn></td>";
                html += "<td class='endpoint-cell'><code class='truncate'>" + h(aptOneLiner(apt + "/" + pr.name, !!pr.is_private)) + "</code><copy-btn data-src='code'></copy-btn></td>";
                html += "<td class='endpoint-cell'><code class='truncate'>brew install pazer/build/" + h(brewFormulaName(pr.name)) + "</code><copy-btn data-src='code'></copy-btn></td>";
                html += "<td class='endpoint-cell'><code class='truncate'>" + h(npmInstall(svc, pr.name, "", !!pr.is_private)) + "</code><copy-btn data-src='code'></copy-btn></td>";
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

// dlMintLink renders a download link for a private project's artifact.
//
// The href is still the artifact's REAL url, never "#". An anchor's href is what the browser copies, shows on hover, and opens in a new tab. A page-local "#" makes each of them useless -- "copy link address" yielded the dashboard's own URL.
const dlMintLink = function (url: string, project: string, version: string, os: string, arch: string, fmt: string, debug: boolean, label: string, title: string): string {
    var call = "App.downloadArtifact(this,'" + project + "','" + version + "','" + os + "','" + arch + "','" + fmt + "'," + (debug ? "true" : "false") + ")";
    return '<a href="' + h(url) + '" class="dl-mint" onclick="return ' + h(call) + '" title="' + h(title) + '">' + h(label) + "</a>";
};

// downloadArtifact mints a temporary signed link for exactly this artifact, then
// triggers the download by clicking a synthetic anchor (same effect as following a
// normal download link). Returns false so the browser does not ALSO follow the
// href, which points at the unsigned url and would ask for credentials.
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
                html += '<td><a href="' + h(siteFilesHash(p.name, s.branch)) + '">Files</a> &middot; <a href="' + h(siteBranchURL(sitesBase, p.name, s.branch)) + '" target="_blank">Open</a></td></tr>';
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

pages.siteFiles = function (name: string, branch: string): void {
    setTitle(name + "@" + branch + " - Sites");
    renderSidebar("sites");
    apiFetch<SiteFilesData>("/projects/" + name + "/site-files?branch=" + encodeURIComponent(branch)).then(function (d) {
        var base = siteBranchURL((d.services || {}).sites || "", d.project.name, branch);
        var files = d.files || [];

        var html = '<h1><a href="#/sites">Sites</a> / <a href="#/sites/' + h(d.project.name) + '">' + h(d.project.name) + "</a> / <code>" + h(branch) + "</code></h1>";
        html += '<div class="card"><table class="data-table"><thead><tr><th>Path</th><th>Size</th></tr></thead><tbody>';
        if (files.length === 0) {
            html += '<tr><td colspan="2" class="empty">No files</td></tr>';
        }
        // Files arrive sorted by path, so a directory row goes in each time the directory changes.
        var prevDirs: string[] = [];
        for (var i = 0; i < files.length; i++) {
            var f = files[i]!;
            var parts = f.path.split("/");
            var dirs = parts.slice(0, -1);
            var same = 0;
            while (same < dirs.length && same < prevDirs.length && dirs[same] === prevDirs[same]) same++;
            for (var j = same; j < dirs.length; j++) {
                html += '<tr><td style="padding-left:' + (j * 1.25 + 0.5) + 'em"><code>' + h(dirs[j]) + "/</code></td><td></td></tr>";
            }
            prevDirs = dirs;
            var url = base + parts.map(encodeURIComponent).join("/");
            html += '<tr><td style="padding-left:' + (dirs.length * 1.25 + 0.5) + 'em"><a href="' + h(url) + '" target="_blank"><code>' + h(parts[parts.length - 1]) + "</code></a></td>";
            html += "<td>" + h(humanSize(f.size)) + "</td></tr>";
        }
        html += "</tbody></table></div>";

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

        html += '<div class="card"><h2>Database Backup</h2>';
        html += '<p>Downloads a consistent copy of the SQLite database, taken while the server keeps running. Blobs are not included.</p>';
        html += '<button class="btn" onclick="App.downloadBackup(this)">Download database backup</button></div>';
        document.getElementById("content")!.innerHTML = html;
    });
};

const downloadBackup = function (btn: HTMLButtonElement): void {
    if (demo) { alert("Could not back up (preview/demo mode has no backend)."); return; }
    btn.disabled = true;
    window.location.href = "/api/backup";
    setTimeout(function () { btn.disabled = false; }, 2500);
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
    html += '<div class="card"><p class="muted">Eviction keeps the newest <strong>keep-N</strong> published releases on each <code>(project, branch)</code> and sweeps abandoned uploads, then deletes any blob nothing else references. Each branch&#39;s latest published build, tagged images, pushed-docker builds, and anything inside the recency guard are <strong>never</strong> deleted. Nothing happens until you run it below (or enable the background sweeper on the <a href=\"#/settings\">Settings</a> page).</p><p>' + sweeper + "</p></div>";

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
    html += '<button class="btn" title="' + h(INVENTORY_HINT) + '" onclick="App.copyInventory(this)">Copy file inventory (JSON)</button> ';
    html += '<button class="btn" title="' + h(INVENTORY_HINT) + '" onclick="App.downloadInventory(this)">Download file inventory (JSON)</button> ';
    html += '<button class="btn btn-danger" onclick="App.runRetention()">Run garbage collection now</button>';
    html += "</div>";
    html += '<p class="muted" style="margin-top:8px">' + h(INVENTORY_HINT) + "</p>";
    html += "</div>";

    html += '<div class="card"><h2>Deleted branches</h2>';
    html += '<p class="muted">Deletes every build on a non-default branch that GitHub confirms is gone from the project&#39;s repo. A branch, a tag, or an open pull request with that name keeps its builds. So does any branch GitHub does not answer for, and anything inside the recency guard. Site deployments are not included: a site stores a sanitized branch name, so GitHub cannot confirm it.</p>';
    html += '<div id="deleted-branches" style="margin-top:16px"></div>';
    html += '<div class="row-actions" style="margin-top:16px">';
    html += '<button class="btn" onclick="App.checkDeletedBranches(this)">Check branches on GitHub</button> ';
    html += '<button class="btn btn-danger" onclick="App.runDeletedBranches(this)">Delete builds of deleted branches</button>';
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

const INVENTORY_HINT = "Every stored file with its size, timestamp, project, version, sha256, reference count, and the reason retention keeps it — plus totals grouped by that reason.";

// The inventory is a debug dump of the server's own state. This never falls
// back to the demo fixture on a failed request: a copied fixture would read as
// what the server holds.
const fetchInventory = function (): Promise<RetentionInventory> {
    if (demo) return Promise.resolve().then(function () { return demoFetch<RetentionInventory>("/retention/inventory"); });
    return fetch("/api/retention/inventory").then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.json();
    });
};

// inventoryAction fetches the inventory, hands the pretty-printed JSON to run,
// and reports the outcome on the button itself.
const inventoryAction = function (btn: HTMLButtonElement, okLabel: string, run: (json: string, inv: RetentionInventory) => void): void {
    var orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "Collecting...";
    fetchInventory().then(function (inv) {
        return Promise.resolve(run(JSON.stringify(inv, null, 2), inv));
    }).then(function () {
        btn.textContent = okLabel;
    }).catch(function (e: Error) {
        btn.textContent = "Failed: " + (e && e.message ? e.message : "error");
    }).then(function () {
        setTimeout(function () {
            btn.textContent = orig;
            btn.disabled = false;
        }, 2500);
    });
};

const copyInventory = function (btn: HTMLButtonElement): void {
    inventoryAction(btn, "Copied", function (json) {
        navigator.clipboard.writeText(json);
    });
};

const downloadInventory = function (btn: HTMLButtonElement): void {
    inventoryAction(btn, "Downloaded", function (json, inv) {
        var stamp = (inv.generated_at || "").replace(/\.\d+/, "").replace(/:/g, "-");
        var url = URL.createObjectURL(new Blob([json], { type: "application/json" }));
        var a = document.createElement("a");
        a.href = url;
        a.download = "buildhost-retention-inventory-" + stamp + ".json";
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
    });
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

const BRANCH_STATE_BADGE: Record<string, string> = { deleted: "danger", present: "success", default: "info", unknown: "warning" };

const renderDeletedBranches = function (rep: DeletedBranchesReport): void {
    var el = document.getElementById("deleted-branches");
    if (!el) return;
    var branches = rep.branches || [];
    var unknown = branches.filter(function (b) { return b.state === "unknown"; }).length;
    var html = '<div class="stat-grid">';
    html += '<div class="stat-card"><div class="stat-value">' + h(rep.release_count || 0) + '</div><div class="stat-label">' + (rep.enforced ? "Builds deleted" : "Builds to delete") + "</div></div>";
    html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(rep.reclaimable_bytes || 0)) + '</div><div class="stat-label">' + (rep.enforced ? "Freed" : "Reclaimable") + "</div></div>";
    html += '<div class="stat-card"><div class="stat-value">' + h(branches.length) + '</div><div class="stat-label">Branches checked</div></div>';
    html += '<div class="stat-card"><div class="stat-value">' + h(unknown) + '</div><div class="stat-label">Not confirmed (kept)</div></div>';
    html += "</div>";
    html += '<table class="data-table"><thead><tr><th>Project</th><th>Repo</th><th>Branch</th><th>Builds</th><th>GitHub</th></tr></thead><tbody>';
    if (branches.length === 0) {
        html += '<tr><td colspan="5" class="empty">No non-default branch builds outside the recency guard</td></tr>';
    }
    for (var i = 0; i < branches.length; i++) {
        var b = branches[i];
        html += "<tr><td><a href='#/projects/" + h(b.project_name) + "'>" + h(b.project_name) + "</a></td>";
        html += "<td>" + h(b.github_repo) + "</td><td>" + h(b.branch) + "</td><td>" + h(b.releases) + "</td>";
        html += "<td>" + badge(BRANCH_STATE_BADGE[b.state] || "neutral", b.state) + (b.error ? ' <span class="muted">' + h(b.error) + "</span>" : "") + "</td></tr>";
    }
    html += "</tbody></table>";
    el.innerHTML = html;
};

// deletedBranchesRequest never falls back to demo data on a failed request:
// a fixture would read as what GitHub answered.
const deletedBranchesRequest = function (btn: HTMLButtonElement, method: string, busy: string): void {
    var orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = busy;
    var req: Promise<DeletedBranchesReport> = demo
        ? Promise.resolve().then(function () { return demoFetch<DeletedBranchesReport>("/retention/deleted-branches"); })
        : fetch("/api/retention/deleted-branches", { method: method }).then(function (res) {
            if (!res.ok) return res.text().then(function (t) { throw new Error(t || "HTTP " + res.status); });
            return res.json();
        });
    req.then(renderDeletedBranches).catch(function (e: Error) {
        alert("Error: " + (e && e.message ? e.message : "request failed"));
    }).then(function () {
        btn.textContent = orig;
        btn.disabled = false;
    });
};

const checkDeletedBranches = function (btn: HTMLButtonElement): void {
    deletedBranchesRequest(btn, "GET", "Asking GitHub...");
};

const runDeletedBranches = function (btn: HTMLButtonElement): void {
    if (demo) { alert("Could not delete (preview/demo mode has no backend)."); return; }
    if (!confirm("Permanently delete every build on a non-default branch that GitHub confirms is gone, and reclaim its storage? This cannot be undone.")) return;
    deletedBranchesRequest(btn, "POST", "Deleting...");
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

        // Health first, and loud.
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

const fail = function (err: unknown): void {
    var msg = err instanceof Error ? err.message : String(err);
    document.getElementById("content")!.innerHTML =
        '<h1>Error</h1><div class="card"><p>This page could not be rendered.</p><pre><code>' +
        h(msg) + "</code></pre></div>";
};

const route = function (): void {
    var hash = window.location.hash.replace(/^#\/?/, "") || "";

    var go = function (page: () => void): void {
        try { page(); } catch (err) { fail(err); }
    };

    var releaseM = hash.match(/^projects\/(.+)\/releases\/([^\/]+)$/);
    if (releaseM) { go(function () { pages.release(releaseM![1], releaseM![2]); }); return; }

    var projectM = hash.match(/^projects\/(.+)$/);
    if (projectM) { go(function () { pages.project(projectM![1]); }); return; }

    var siteFilesM = hash.match(/^sites\/(.+)\/-\/files\/([^\/]+)$/);
    if (siteFilesM) { go(function () { pages.siteFiles(siteFilesM![1], decodeURIComponent(siteFilesM![2])); }); return; }

    var siteM = hash.match(/^sites\/(.+)$/);
    if (siteM) { go(function () { pages.site(siteM![1]); }); return; }

    var first = hash.split("/")[0];
    if (first === "projects") { go(pages.projects); }
    else if (first === "registries") { go(pages.registries); }
    else if (first === "sites") { go(pages.sites); }
    else if (first === "tokens") { go(pages.tokens); }
    else if (first === "oidc") { go(pages.oidc); }
    else if (first === "artifacts") { go(pages.artifacts); }
    else if (first === "storage") { go(pages.storage); }
    else if (first === "retention") { go(pages.retention); }
    else if (first === "goproxy") { go(pages.goproxy); }
    else if (first === "run-files") { go(pages.runFiles); }
    else if (first === "cache") { go(pages.cache); }
    else if (first === "mirror") { go(pages.mirror); }
    else if (first === "duplicates") { go(pages.duplicates); }
    else if (first === "github") { go(pages.github); }
    else if (first === "settings") { go(pages.settings); }
    else { go(pages.dashboard); }
};

// --- Demo data ---

// Live runs, ended runs inside the keep window, and one past it. Every
// treatment of the Run Files charts is on screen in a preview.
const demoRunFiles = function (): RunFilesData {
    const ago = function (hours: number): string { return new Date(Date.now() - hours * 3600000).toISOString(); };
    const sha = function (seed: string): string {
        let out = "";
        for (let i = 0; out.length < 64; i++) out += ((seed.charCodeAt(i % seed.length) * 7919 + i * 104729) % 65536).toString(16).padStart(4, "0");
        return out.slice(0, 64);
    };
    const run = function (repo: string, id: string, start: number, end: number | null, files: [string, number, number, number][]): RunFilesData["runs"][number] {
        let bytes = 0;
        let reads = 0;
        const list = files.map(function (f) {
            bytes += f[1];
            reads += f[3];
            return { name: f[0], size: f[1], sha256: sha(repo + id + f[0]), created_at: ago(f[2]), reads: f[3], read_at: f[3] ? ago(Math.max(0, f[2] - 0.3)) : null };
        });
        return {
            repo: repo, run_id: id, created_at: ago(start), used_at: ago(end == null ? 0.05 : end + 0.1), checked_at: ago(0.06),
            state: end == null ? "live" as const : "ended" as const,
            ended_at: end == null ? null : ago(end), expires_at: end == null ? null : ago(end - 24),
            bytes: bytes, reads: reads, files: list
        };
    };
    const runs = [
        run("myorg/compiler", "18300112233", 0.6, null, [["toolchain-Linux", 1_200_000_000, 0.5, 6], ["toolchain-macOS", 1_150_000_000, 0.45, 3], ["ape-binary-Linux", 300_000_000, 0.2, 0]]),
        run("myorg/registry", "9120044817", 0.3, null, [["frontend-dist", 48_000_000, 0.25, 2]]),
        run("myorg/compiler", "18299001122", 9, 7.5, [["toolchain-Linux", 1_210_000_000, 8.8, 7], ["toolchain-Windows", 1_300_000_000, 8.6, 2], ["distpack-linux-amd64", 400_000_000, 7.7, 1]]),
        run("myorg/registry", "9119983001", 15, 14.6, [["frontend-dist", 47_500_000, 14.9, 1]]),
        run("myorg/webhook-runner", "4410982211", 21, 20.2, [["dashboard-assets", 12_000_000, 20.9, 1], ["image-layout", 640_000_000, 20.5, 1]])
    ];
    const unreadable = run("myorg/tools", "7730015521", 5, null, [["tool-bundle", 210_000_000, 4.9, 2]]);
    unreadable.state = "unknown";
    unreadable.check_error = "GitHub answered 401 for run myorg/tools#7730015521: unauthorized: missing Authorization header";
    unreadable.used_at = ago(4.5);
    unreadable.expires_at = ago(4.5 - 24);
    runs.push(unreadable);
    let total = 0;
    for (let i = 0; i < runs.length; i++) total += runs[i].bytes;
    return {
        keep_seconds: 86400,
        check_interval_seconds: 300,
        min_free_percent: 10,
        total_bytes: total,
        disk_total: 500_000_000_000,
        disk_used: 438_000_000_000,
        disk_free: 62_000_000_000,
        runs: runs,
        janitor: {
            at: ago(0.06), duration_ms: 412, deferred: false, checked: 3, check_failures: 1, last_check_error: unreadable.check_error,
            ended: 0, deleted: 1, pressure_deleted: 0, freed_bytes: 2_150_000_000
        }
    };
};

// Entries on master and on a pull request, one restored often and one near its expiry.
const demoCache = function (): CacheData {
    const ago = function (hours: number): string { return new Date(Date.now() - hours * 3600000).toISOString(); };
    const keep = 7 * 24;
    const entry = function (repo: string, ref: string, key: string, size: number, created: number, used: number, reads: number): CacheEntryRow {
        return {
            repo: repo, ref: ref, key: key, size: size, sha256: (key + "0".repeat(64)).replace(/[^0-9a-f]/g, "a").slice(0, 64),
            created_at: ago(created), used_at: ago(used), reads: reads, expires_at: ago(used - keep)
        };
    };
    const entries = [
        entry("myorg/compiler", "refs/heads/master", "dist-bootstrap-Linux-X64-9f1c2e", 410_000_000, 30, 0.2, 41),
        entry("myorg/compiler", "refs/heads/master", "wasmtime-modules-wasip1-9f1c2e", 4_600_000_000, 30, 1.5, 12),
        entry("myorg/compiler", "refs/pull/341/merge", "dist-bootstrap-Linux-X64-17bcab", 405_000_000, 9, 9, 0),
        entry("myorg/registry", "refs/heads/main", "node-modules-3a77e1", 220_000_000, 160, 150, 3)
    ];
    let total = 0;
    for (const e of entries) total += e.size;
    return {
        keep_seconds: keep * 3600,
        check_interval_seconds: 300,
        min_free_percent: 15,
        total_bytes: total,
        disk_total: 500_000_000_000,
        disk_used: 438_000_000_000,
        disk_free: 62_000_000_000,
        entries: entries,
        janitor: { at: ago(0.06), duration_ms: 38, deferred: false, entries: 5, expired: 1, pressure_deleted: 0, freed_bytes: 180_000_000 }
    };
};

const demoMirror = function (): MirrorData {
    const ago = function (hours: number): string { return new Date(Date.now() - hours * 3600000).toISOString(); };
    const keep = 7 * 24;
    const digest = function (seed: string): string { return "sha256:" + (seed + "0".repeat(64)).replace(/[^0-9a-f]/g, "c").slice(0, 64); };
    const blob = function (upstream: string, seed: string, mediaType: string, size: number, created: number, used: number, reads: number): MirrorBlobRow {
        return { upstream: upstream, digest: digest(seed), media_type: mediaType, size: size, created_at: ago(created), used_at: ago(used), reads: reads, expires_at: ago(used - keep) };
    };
    const blobs = [
        blob("dockerhub", "a1pine", "application/vnd.oci.image.index.v1+json", 9_218, 50, 0.1, 212),
        blob("dockerhub", "a1pine1ayer", "", 3_640_000, 50, 0.1, 212),
        blob("ghcr", "runner", "application/vnd.oci.image.manifest.v1+json", 2_104, 20, 2, 33),
        blob("ghcr", "runner1ayer", "", 210_000_000, 20, 2, 33)
    ];
    let total = 0;
    for (const b of blobs) total += b.size;
    return {
        keep_seconds: keep * 3600,
        check_interval_seconds: 300,
        tag_ttl_seconds: 60,
        min_free_percent: 15,
        total_bytes: total,
        disk_total: 500_000_000_000,
        disk_used: 438_000_000_000,
        disk_free: 62_000_000_000,
        upstreams: [{ service: "dockerhub", remote: "https://registry-1.docker.io" }, { service: "ghcr", remote: "https://ghcr.io" }],
        blobs: blobs,
        tags: [
            { upstream: "dockerhub", repository: "library/alpine", tag: "latest", digest: digest("a1pine"), media_type: "application/vnd.oci.image.index.v1+json", checked_at: ago(0.1) },
            { upstream: "ghcr", repository: "actions/actions-runner", tag: "2.330.0", digest: digest("runner"), media_type: "application/vnd.oci.image.manifest.v1+json", checked_at: ago(2) }
        ],
        janitor: { at: ago(0.06), duration_ms: 12, deferred: false, blobs: 5, expired: 1, pressure_deleted: 0, freed_bytes: 52_000_000 }
    };
};

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
    "/projects/cli-tool": {
        project: { id: 2, name: "cli-tool", description: "CLI utility", versioning: "semver", is_private: true, created_at: new Date(Date.now() - 864e5 * 10).toISOString(), updated_at: new Date(Date.now() - 86400000).toISOString() },
        releases: [{ version: "1.2.0", git_branch: "release", git_commit: "fff000", published: true, artifact_count: 1, published_at: new Date(Date.now() - 86400000).toISOString(), created_at: new Date(Date.now() - 86400000).toISOString() }],
        sites: [{ branch: "main", file_count: 8, size: 23000, git_commit: "fff000111222", updated_at: new Date(Date.now() - 86400000).toISOString() }],
        base_url: "https://builds.example.com",
        services: demoServices
    },
    "/projects/myapp/releases/3": {
        project: { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, created_at: new Date(Date.now() - 864e5 * 30).toISOString(), updated_at: new Date(Date.now() - 3600000).toISOString() },
        release: { version: "3", published: true, git_branch: "main", git_commit: "abc123", notes: "", published_at: new Date(Date.now() - 3600000).toISOString(), created_at: new Date(Date.now() - 3600000).toISOString() },
        artifacts: [
            { os: "linux", arch: "amd64", kind: "binary", filename: "myapp", size: 15728640, download_count: 42, debug_storage_key: "", exe_format: "elf", platforms: [], packages: [] },
            { os: "darwin", arch: "arm64", kind: "binary", filename: "myapp", size: 14680064, download_count: 18, debug_storage_key: "", exe_format: "macho", platforms: [], packages: [] }
        ],
        total_downloads: 60, total_size: 30408704,
        base_url: "https://builds.example.com",
        services: demoServices
    },
    "/projects/cli-tool/releases/1.2.0": {
        project: { id: 2, name: "cli-tool", description: "CLI utility", versioning: "semver", is_private: true, created_at: new Date(Date.now() - 864e5 * 10).toISOString(), updated_at: new Date(Date.now() - 86400000).toISOString() },
        release: { version: "1.2.0", published: true, git_branch: "release", git_commit: "fff000", notes: "", published_at: new Date(Date.now() - 86400000).toISOString(), created_at: new Date(Date.now() - 86400000).toISOString() },
        artifacts: [
            { os: "linux", arch: "amd64", kind: "binary", filename: "cli-tool", size: 10485760, download_count: 7, debug_storage_key: "", exe_format: "elf", platforms: [], packages: [] }
        ],
        total_downloads: 7, total_size: 10485760,
        base_url: "https://builds.example.com",
        services: demoServices
    },
    "/visibility-folders": [],
    "/registries": { base_url: "https://builds.example.com", services: demoServices, projects: [{ name: "myapp", is_private: false }, { name: "cli-tool", is_private: true }] },
    "/sites": { sites: [{ project_name: "myapp", branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { project_name: "myapp", branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }, { project_name: "cli-tool", branch: "main", file_count: 8, size: 23000, git_commit: "fff000111222", updated_at: new Date(Date.now() - 86400000).toISOString() }], base_url: "https://builds.example.com", services: demoServices },
    "/run-files": demoRunFiles(),
    "/cache": demoCache(),
    "/mirror": demoMirror(),
    // The demo shows a repo mid-rename, which is the state this page exists for.
    "/duplicates": {
        groups: [{
            repo_id: "1287579802",
            repo: "myorg/cli-tool",
            projects: [
                { name: "old-cli-name/cli-tool", root: "old-cli-name", repo: "myorg/old-cli-name", releases: 31 },
                { name: "cli-tool", root: "cli-tool", repo: "myorg/cli-tool", releases: 19 }
            ]
        }]
    },
    // The demo deliberately shows an UNHEALTHY proxy: the failure mode this
    // page exists for (a credential that cannot read private modules while
    // public ones keep working) is the worth showing off in a preview.
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
    "/github-credential": {
        source: "token", env_credential: false, github_api: "https://github-state-mirror.pazer.io",
        secret_server: { configured: true, ok: true, applied: "token", at: "2026-10-25T14:00:00Z" }
    },
    "/settings": {
        can_restart: true,
        settings: [
            { name: "BUILDHOST_PRIMARY_DOMAIN", group: "Domains", kind: "string", default: "", description: "The apex that owns the web UI.", env_only: false, live: false, value: "example.com", set: true, source: "environment", restart_pending: false },
            { name: "BUILDHOST_SECRET_SERVER_TOKEN", group: "GitHub", kind: "secret", default: "", description: "secret-server machine token.", env_only: false, live: false, value: "", set: true, source: "dashboard", restart_pending: true },
            { name: "BUILDHOST_RUN_FILES_KEEP", group: "Janitors", kind: "duration", default: "24h", description: "How long a run's files outlive the run.", env_only: false, live: false, value: "", set: false, source: "default", restart_pending: false },
            { name: "BUILDHOST_MAX_UPLOAD_SIZE", group: "Uploads", kind: "bytes", default: "2G", description: "Cap on one REST artifact upload.", env_only: false, live: true, value: "4G", set: true, source: "dashboard", restart_pending: false },
            { name: "BUILDHOST_DATA_DIR", group: "Server", kind: "string", default: "./data", description: "Blob store and temp directory.", env_only: true, live: false, value: "/data", set: true, source: "environment", restart_pending: false }
        ]
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
    },
    "/retention/deleted-branches": {
        enforced: false, release_count: 2, keep_n_count: 0, abandoned_count: 0,
        blobs: 2, blobs_retained: 0, reclaimable_bytes: 12582912,
        releases: [
            { project_name: "myapp", project_id: 1, branch: "feature-x", version: "5", reason: "deleted-branch" },
            { project_name: "myapp", project_id: 1, branch: "feature-x", version: "4", reason: "deleted-branch" }
        ],
        branches: [
            { project_name: "myapp", github_repo: "myorg/myapp", branch: "feature-x", state: "deleted", error: "", releases: 2 },
            { project_name: "myapp", github_repo: "myorg/myapp", branch: "release", state: "present", error: "", releases: 3 },
            { project_name: "cli-tool", github_repo: "myorg/cli-tool", branch: "fix-y", state: "unknown", error: "GitHub answered 403 for myorg/cli-tool/branches/fix-y", releases: 1 }
        ]
    },
    "/retention/inventory": {
        generated_at: new Date().toISOString(),
        policy: { keep_n: 10, recency_hours: 24, recency_cutoff: new Date(Date.now() - 86400000).toISOString() },
        totals: {
            files: 4, bytes: 60817408, blobs: 3, blob_bytes: 46137344,
            reclaimable_blobs: 1, reclaimable_bytes: 10485760,
            held_blobs: 2, held_bytes: 35651584,
            releases: 5, evicted_releases: 3, hold_mismatches: 0
        },
        by_hold: [
            { name: "branch-tip", files: 1, bytes: 25165824, blobs: 1, blob_bytes: 25165824 },
            { name: "shared-blob", files: 2, bytes: 25165824, blobs: 1, blob_bytes: 10485760 },
            { name: "reclaimable", files: 1, bytes: 10485760, blobs: 1, blob_bytes: 10485760 }
        ],
        by_role: [
            { name: "artifact", files: 3, bytes: 46137344, blobs: 2, blob_bytes: 35651584 },
            { name: "debug", files: 1, bytes: 14680064, blobs: 1, blob_bytes: 10485760 }
        ],
        files: [
            {
                storage_key: "d1e5a0c2", sha256: "d1e5a0c2", role: "artifact", size: 25165824,
                created_at: new Date(Date.now() - 3600000).toISOString(), project: "myapp", project_id: 1,
                release_id: 9, version: "9", branch: "main", os: "linux", arch: "amd64", kind: "binary",
                filename: "myapp", published: true, refs: 1, reclaimable: false, hold: "branch-tip"
            },
            {
                storage_key: "9f4b7712", sha256: "9f4b7712", role: "artifact", size: 14680064,
                created_at: new Date(Date.now() - 604800000).toISOString(), project: "myapp", project_id: 1,
                release_id: 7, version: "7", branch: "main", os: "linux", arch: "amd64", kind: "binary",
                filename: "myapp", published: true, refs: 2, reclaimable: false, hold: "shared-blob"
            },
            {
                storage_key: "9f4b7712", sha256: "9f4b7712", role: "artifact", size: 14680064,
                created_at: new Date(Date.now() - 259200000).toISOString(), project: "cli-tool", project_id: 2,
                release_id: 4, version: "1.2.0", branch: "release", os: "linux", arch: "amd64", kind: "binary",
                filename: "cli-tool", published: true, refs: 2, reclaimable: false, hold: "shared-blob"
            },
            {
                storage_key: "5c33ae91", role: "debug", size: 10485760,
                created_at: new Date(Date.now() - 604800000).toISOString(), project: "myapp", project_id: 1,
                release_id: 7, version: "7", branch: "main", os: "linux", arch: "amd64", kind: "binary",
                filename: "myapp", published: true, refs: 1, reclaimable: true, hold: ""
            }
        ]
    }
};

// Each demo site branch gets a file list, so every Files link in the preview reaches a page.
for (const name of ["myapp", "cli-tool"]) {
    const pd = demoData["/projects/" + name] as ProjectData;
    for (const site of pd.sites) {
        demoData["/projects/" + name + "/site-files?branch=" + encodeURIComponent(site.branch)] = {
            services: demoServices,
            project: pd.project,
            site: site,
            files: [
                { path: "404.html", size: 512 },
                { path: "assets/css/site.css", size: 2048 },
                { path: "assets/js/app.js", size: 18432 },
                { path: "index.html", size: 4096 }
            ]
        };
    }
}

// --- Init ---

// --- Duplicates left by a GitHub rename ---

const renderMergePlan = function (p: MergePlan): string {
    var rows = "";
    for (var i = 0; i < p.releases.length; i++) {
        var r = p.releases[i];
        rows += "<tr><td>" + h(r.old_version) + "</td><td>&rarr; " + h(r.new_version) + "</td></tr>";
    }
    var html = "<h3>Merging " + h(p.from) + " into " + h(p.into) + "</h3>";
    if (p.conflicts.length) {
        html += '<div class="card"><p><strong>This merge is blocked:</strong></p><ul>';
        for (var c = 0; c < p.conflicts.length; c++) html += "<li>" + h(p.conflicts[c]) + "</li>";
        html += "</ul></div>";
        return html;
    }
    html += "<p>" + p.releases.length + " releases move and are renumbered. " +
        "Sites: " + p.sites + ", OCI tags: " + p.oci_tags + ", blob links: " + p.oci_blobs +
        ", tokens: " + p.tokens + ", policies: " + p.policies + ".</p>";
    html += "<p>Kept resolving as aliases of " + h(p.into) + ": <code>" + h(p.aliases.join(", ")) + "</code></p>";
    if (rows) html += '<table class="table"><thead><tr><th>was</th><th>becomes</th></tr></thead><tbody>' + rows + "</tbody></table>";
    html += '<p><button class="btn btn-danger" onclick="App.applyMerge(\'' +
        h(p.from) + "', '" + h(p.into) + '\')">Merge ' + h(p.from) + " into " + h(p.into) + "</button></p>";
    return html;
};

const previewMerge = function (from: string, into: string): void {
    apiFetch<MergePlan>("/projects/" + encodeURIComponent(from) + "/merge-plan?into=" + encodeURIComponent(into))
        .then(function (p) {
            document.getElementById("merge-plan")!.innerHTML = renderMergePlan(p);
        });
};

const applyMerge = function (from: string, into: string): void {
    if (!confirm("Merge " + from + " into " + into + "? A database snapshot is written first, but the release renumbering cannot be undone without restoring it.")) return;
    fetch("/api/projects/" + encodeURIComponent(from) + "/merge", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ into: into })
    }).then(function (res) {
        return res.json().then(function (p: MergePlan) {
            if (!res.ok || !p.applied) {
                document.getElementById("merge-plan")!.innerHTML = renderMergePlan(p);
                return;
            }
            alert("Merged " + p.from + " into " + p.into + ".\nSnapshot: " + (p.snapshot || "(none)"));
            pages.duplicates();
        });
    }).catch(function () { alert("Could not merge (preview/demo mode has no backend)."); });
};

const setVersioning = function (name: string, versioning: string): void {
    if (!confirm("Change " + name + " versioning to " + versioning + "?")) {
        pages.project(name);
        return;
    }
    fetch("/api/projects/" + encodeURIComponent(name) + "/versioning", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ versioning: versioning })
    }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Error: " + t); pages.project(name); });
        pages.project(name);
    }).catch(function () { alert("Could not change versioning (preview/demo mode has no backend)."); pages.project(name); });
};

pages.runFiles = function (): void {
    setTitle("Run Files");
    renderSidebar("run-files");
    Promise.all([apiFetch<RunFilesData>("/run-files"), loadRunFilesCharts()]).then(function (res) {
        renderRunFiles(res[0]);
    });
};

// The charts are js-snippets components, loaded at run time from the library site.
const loadRunFilesCharts = function (): Promise<unknown> {
    return Promise.all([
        import("https://sites.pazer.build/js-snippets/@library/ui/timeline-view.js"),
        import("https://sites.pazer.build/js-snippets/@library/ui/data-table.js"),
        import("https://sites.pazer.build/js-snippets/@library/ui/bar-chart.js")
    ]);
};

const runURL = function (run: RunFilesRun): string {
    return "https://github.com/" + run.repo + "/actions/runs/" + run.run_id;
};

const runLabel = function (run: RunFilesRun): string {
    return run.repo + " #" + run.run_id;
};

const ms = function (s: string | null): number {
    return s ? new Date(s).getTime() : 0;
};

const RF_RUN_COLOR = "#4a9eff";

// A table cell has no room for timeAgo's "18 minutes ago".
const shortAgo = function (iso: string): string {
    const mins = Math.max(0, Math.round((Date.now() - ms(iso)) / 60000));
    if (mins < 60) return mins + "m ago";
    if (mins < 48 * 60) return Math.round(mins / 60) + "h ago";
    return Math.round(mins / 1440) + "d ago";
};

const renderRunFiles = function (d: RunFilesData): void {
    const now = Date.now();
    let live = 0;
    let unknown = 0;
    let files = 0;
    let reads = 0;
    for (const run of d.runs) {
        if (run.state === "live") live++;
        if (run.state === "unknown") unknown++;
        files += run.files.length;
        reads += run.reads;
    }
    const keepHours = Math.round(d.keep_seconds / 3600);
    let html = "<h1>Run Files</h1>";
    html += '<p class="rf-lede">Directories one job of a CI run hands to later jobs through <code>buildhost-handoff</code>. ' +
        "They go " + h(keepHours) + "h after GitHub reports the run complete, sooner (oldest ended first) while free disk is under " +
        h(d.min_free_percent) + "%. A live run's files never go early.</p>";
    html += '<div class="rf-kpis">' +
        kpi(d.runs.length, "runs") + kpi(live, "live") + (unknown ? kpi(unknown, "status unknown") : "") + kpi(files, "files") + kpi(humanSize(d.total_bytes), "stored") + kpi(reads, "reads") +
        "</div>";
    html += janitorLine(d);
    html += '<section class="rf-panel"><h2>Disk</h2>' +
        (d.disk_total ? '<bar-chart id="rf-disk"></bar-chart>' : '<p class="empty">The data directory reports no filesystem size.</p>') + "</section>";
    if (!d.runs.length) {
        html += '<section class="rf-panel"><p class="empty">No CI run holds run files.</p></section>';
        document.getElementById("content")!.innerHTML = html;
        fillDiskChart("rf-disk", "run files", d);
        return;
    }
    html += '<section class="rf-panel"><h2>Lifecycle <span class="rf-hint">solid: run live &middot; hatched: kept after the run ended &middot; pips: file sent</span></h2>' +
        '<timeline-view id="rf-timeline" no-minimap></timeline-view></section>';
    html += '<div class="rf-split">' +
        '<section class="rf-panel"><h2>Runs</h2><data-table id="rf-runs"></data-table></section>' +
        '<section class="rf-panel"><h2>Bytes per run</h2><bar-chart id="rf-bytes" show-values></bar-chart></section>' +
        "</div>";
    document.getElementById("content")!.innerHTML = html;
    fillDiskChart("rf-disk", "run files", d);
    fillTimeline(d, now);
    fillRunsTable(d);
    fillBytesChart(d);
};

const kpi = function (value: string | number, label: string): string {
    return '<span class="rf-kpi"><b>' + h(value) + "</b> " + h(label) + "</span>";
};

const janitorLine = function (d: RunFilesData): string {
    const j = d.janitor;
    const every = "sweeps every " + Math.round(d.check_interval_seconds / 60) + "m";
    if (!j) return '<p class="rf-janitor">Janitor: no sweep yet since this server started (' + every + ").</p>";
    if (j.deferred) return '<p class="rf-janitor">Janitor: last sweep ' + h(timeAgo(j.at)) + " was deferred, writes in flight (" + every + ").</p>";
    let s = "Janitor: last sweep " + h(timeAgo(j.at)) + " in " + h(j.duration_ms) + "ms, asked GitHub about " + h(j.checked) + " live run" + (j.checked === 1 ? "" : "s") +
        " (" + h(j.ended) + " ended), deleted " + h(j.deleted) + (j.pressure_deleted ? " (" + h(j.pressure_deleted) + " for disk pressure)" : "") +
        ", freed " + h(humanSize(j.freed_bytes)) + " &middot; " + every;
    if (j.check_failures) s += ' &middot; <span class="rf-bad">' + h(j.check_failures) + " status check" + (j.check_failures === 1 ? "" : "s") + " failed: " + h(j.last_check_error) + "</span>";
    if (j.error) s += ' &middot; <span class="rf-bad">sweep failed: ' + h(j.error) + "</span>";
    return '<p class="rf-janitor">' + s + "</p>";
};

type DiskShare = Pick<RunFilesData, "disk_total" | "disk_used" | "disk_free" | "total_bytes" | "min_free_percent">;

const fillDiskChart = function (id: string, label: string, d: DiskShare): void {
    const el = document.getElementById(id) as BarChartElement | null;
    if (!el || !d.disk_total) return;
    const used = d.disk_used ?? d.disk_total - (d.disk_free ?? 0);
    el.format = humanSize;
    el.max = d.disk_total;
    el.series = [
        { key: "run", label: label, color: RF_RUN_COLOR },
        { key: "other", label: "everything else", color: "#6b7178" },
        { key: "free", label: "free", muted: true }
    ];
    el.rows = [{ id: "disk", label: "data dir", values: { run: d.total_bytes, other: Math.max(0, used - d.total_bytes), free: d.disk_free ?? 0 } }];
    el.markers = [{ value: d.disk_total * (1 - d.min_free_percent / 100), label: d.min_free_percent + "% free floor", kind: "warning" }];
};

const fillTimeline = function (d: RunFilesData, now: number): void {
    const el = document.getElementById("rf-timeline") as TimelineViewElement | null;
    if (!el) return;
    const lanes: TimelineLane[] = [];
    const intervals: TimelineInterval[] = [];
    let first = now;
    let last = now;
    for (const run of d.runs) {
        const lane = run.repo + "#" + run.run_id;
        const start = ms(run.created_at);
        const ended = run.ended_at ? ms(run.ended_at) : null;
        const expires = run.expires_at ? ms(run.expires_at) : null;
        first = Math.min(first, start);
        if (expires) last = Math.max(last, expires);
        lanes.push({ id: lane, label: runLabel(run), group: run.repo });
        // The chart is static (maxTime is set), so a live run ends at now rather than running on to the right edge.
        intervals.push({
            id: lane, laneId: lane, start: start, end: expires ?? now, category: run.repo, label: humanSize(run.bytes),
            segments: ended != null && expires != null ? [{ start: start, end: ended, kind: "" }, { start: ended, end: expires, kind: "hatch" }] : undefined,
            data: run
        });
        for (const f of run.files) {
            const t = ms(f.created_at);
            intervals.push({ id: lane + "/" + f.name, laneId: lane, start: t, end: t, category: run.repo, label: f.name, data: f });
        }
    }
    el.staleAfterMs = 0;
    el.tooltipFor = function (hit: TimelineHit): string | null {
        const data = hit.interval?.data as RunFilesRun | RunFile | undefined;
        if (!data) return null;
        if ("run_id" in data) {
            return runLabel(data) + "\n" + humanSize(data.bytes) + " in " + data.files.length + " file" + (data.files.length === 1 ? "" : "s") +
                "\nstarted " + formatTime(data.created_at) +
                (data.ended_at ? "\nended " + formatTime(data.ended_at) + "\ndeleted after " + formatTime(data.expires_at) :
                    data.state === "unknown" ? "\nstatus unknown: " + data.check_error + "\ndeleted after " + formatTime(data.expires_at) :
                    "\nlive, last used " + timeAgo(data.used_at));
        }
        return data.name + "\n" + humanSize(data.size) + ", sent " + formatTime(data.created_at) +
            "\nread " + data.reads + " time" + (data.reads === 1 ? "" : "s") + (data.read_at ? ", last " + timeAgo(data.read_at) : "");
    };
    // A fixed window from the oldest run to the last deletion shows each keep window in full; a live chart stops at now.
    const pad = (last - first) * 0.03;
    el.minTime = first - pad;
    el.maxTime = last + pad;
    el.setData({ lanes: lanes, intervals: intervals, markers: [{ time: now, label: "now" }] });
    el.setViewport(first - pad, last + pad);
    el.style.height = Math.min(560, 64 + lanes.length * 40) + "px";
};

interface RunTableRow {
    key: string;
    run: RunFilesRun;
}

const fillRunsTable = function (d: RunFilesData): void {
    const el = document.getElementById("rf-runs") as DataTableElement<RunTableRow> | null;
    if (!el) return;
    const link = function (row: RunTableRow): Node {
        const a = document.createElement("a");
        a.href = runURL(row.run);
        a.target = "_blank";
        a.rel = "noopener";
        a.textContent = runLabel(row.run);
        return a;
    };
    const when = function (iso: string | null, empty: string): (row: RunTableRow) => Node {
        return function (): Node {
            const span = document.createElement("span");
            span.textContent = iso ? shortAgo(iso) : empty;
            if (iso) span.title = formatTime(iso);
            return span;
        };
    };
    el.columns = [
        { key: "run", label: "Run", value: (r) => runLabel(r.run), render: link },
        { key: "state", label: "State", value: (r) => r.run.state, render: stateNode },
        { key: "created", label: "Started", value: (r) => ms(r.run.created_at), render: (r) => when(r.run.created_at, "")(r), text: (r) => r.run.created_at },
        { key: "expires", label: "Deleted", value: (r) => ms(r.run.expires_at) || Infinity, render: (r) => untilNode(r.run.expires_at), text: (r) => r.run.expires_at ?? "" },
        { key: "files", label: "Files", value: (r) => r.run.files.length, align: "end" },
        { key: "bytes", label: "Size", value: (r) => r.run.bytes, render: (r) => humanSize(r.run.bytes), align: "end" },
        { key: "reads", label: "Reads", value: (r) => r.run.reads, align: "end" }
    ];
    el.facets = [{ key: "state", label: "state", of: (r) => r.run.state, order: ["live", "unknown", "ended"] }];
    el.rowId = (r) => r.key;
    el.sort = { key: "created", dir: "desc" };
    el.detailFor = function (row: RunTableRow): Node {
        const t = document.createElement("table");
        t.className = "rf-files";
        t.innerHTML = "<thead><tr><th>File</th><th>Size</th><th>Sent</th><th>Reads</th><th>Last read</th><th>sha256</th></tr></thead>";
        const body = document.createElement("tbody");
        for (const f of row.run.files) {
            const tr = document.createElement("tr");
            tr.innerHTML = "<td><code>" + h(f.name) + "</code></td><td>" + h(humanSize(f.size)) + '</td><td title="' + h(formatTime(f.created_at)) + '">' +
                h(timeAgo(f.created_at)) + "</td><td>" + h(f.reads) + '</td><td title="' + h(formatTime(f.read_at)) + '">' + h(f.read_at ? timeAgo(f.read_at) : "never") +
                '</td><td><code title="' + h(f.sha256) + '">' + h(f.sha256.slice(0, 12)) + "</code></td>";
            body.append(tr);
        }
        t.append(body);
        return t;
    };
    el.rows = d.runs.map((run) => ({ key: run.repo + "#" + run.run_id, run: run }));
};

// An unknown run carries the status check error as its tooltip.
const stateNode = function (r: RunTableRow): Node {
    const span = document.createElement("span");
    span.textContent = r.run.state;
    if (r.run.state === "unknown") {
        span.className = "rf-bad";
        span.title = "GitHub could not report this run's status: " + (r.run.check_error ?? "");
    }
    return span;
};

const untilNode = function (iso: string | null): Node {
    const span = document.createElement("span");
    if (!iso) {
        span.textContent = "after end";
        span.className = "rf-dim";
        return span;
    }
    const left = ms(iso) - Date.now();
    span.title = formatTime(iso);
    span.textContent = left <= 0 ? "next sweep" : "in " + (left >= 3600000 ? Math.round(left / 3600000) + "h" : Math.max(1, Math.round(left / 60000)) + "m");
    return span;
};

const fillBytesChart = function (d: RunFilesData): void {
    const el = document.getElementById("rf-bytes") as BarChartElement | null;
    if (!el) return;
    el.format = humanSize;
    el.series = [
        { key: "live", label: "live run", color: RF_RUN_COLOR },
        { key: "unknown", label: "status unknown", color: "#f59e0b" },
        { key: "ended", label: "ended, kept", color: "#a78bfa" }
    ];
    el.rows = d.runs.slice().sort((a, b) => b.bytes - a.bytes).map(function (run) {
        const values: Record<string, number> = {};
        values[run.state] = run.bytes;
        return { id: run.repo + "#" + run.run_id, label: runLabel(run), values: values, href: runURL(run), detail: run.files.length + " files" };
    });
};

pages.cache = function (): void {
    setTitle("CI Cache");
    renderSidebar("cache");
    Promise.all([apiFetch<CacheData>("/cache"), loadRunFilesCharts()]).then(function (res) {
        renderCache(res[0]);
    });
};

const renderCache = function (d: CacheData): void {
    const repos = new Set<string>();
    let reads = 0;
    for (const e of d.entries) {
        repos.add(e.repo);
        reads += e.reads;
    }
    const keepDays = Math.round(d.keep_seconds / 86400);
    let html = "<h1>CI Cache</h1>";
    html += '<p class="rf-lede">Directories a CI job saves with <code>buildhost-cache-save</code> for later runs to restore with <code>buildhost-cache-restore</code>. ' +
        "A run reads the entries of its own ref and of the default branch. An entry nothing restores for " + h(keepDays) +
        "d goes, and the least recently used go sooner while free disk is under " + h(d.min_free_percent) + "%.</p>";
    html += '<div class="rf-kpis">' +
        kpi(d.entries.length, "entries") + kpi(repos.size, "repositories") + kpi(humanSize(d.total_bytes), "stored") + kpi(reads, "restores") +
        "</div>";
    html += cacheJanitorLine(d);
    html += '<section class="rf-panel"><h2>Disk</h2>' +
        (d.disk_total ? '<bar-chart id="cache-disk"></bar-chart>' : '<p class="empty">The data directory reports no filesystem size.</p>') + "</section>";
    html += d.entries.length
        ? '<section class="rf-panel"><h2>Entries</h2><data-table id="cache-entries"></data-table></section>'
        : '<section class="rf-panel"><p class="empty">No cache entry is stored.</p></section>';
    document.getElementById("content")!.innerHTML = html;
    fillDiskChart("cache-disk", "cache", d);
    fillCacheTable(d);
};

const cacheJanitorLine = function (d: CacheData): string {
    const j = d.janitor;
    const every = "sweeps every " + Math.round(d.check_interval_seconds / 60) + "m";
    if (!j) return '<p class="rf-janitor">Janitor: no sweep yet since this server started (' + every + ").</p>";
    if (j.deferred) return '<p class="rf-janitor">Janitor: last sweep ' + h(timeAgo(j.at)) + " was deferred, writes in flight (" + every + ").</p>";
    let s = "Janitor: last sweep " + h(timeAgo(j.at)) + " in " + h(j.duration_ms) + "ms over " + h(j.entries) + " entries, expired " + h(j.expired) +
        (j.pressure_deleted ? ", deleted " + h(j.pressure_deleted) + " for disk pressure" : "") + ", freed " + h(humanSize(j.freed_bytes)) + " &middot; " + every;
    if (j.error) s += ' &middot; <span class="rf-bad">sweep failed: ' + h(j.error) + "</span>";
    return '<p class="rf-janitor">' + s + "</p>";
};

const fillCacheTable = function (d: CacheData): void {
    const el = document.getElementById("cache-entries") as DataTableElement<CacheEntryRow> | null;
    if (!el) return;
    const ago = function (iso: string): Node {
        const span = document.createElement("span");
        span.textContent = shortAgo(iso);
        span.title = formatTime(iso);
        return span;
    };
    el.columns = [
        { key: "repo", label: "Repository", value: (e) => e.repo },
        { key: "ref", label: "Ref", value: (e) => e.ref },
        { key: "key", label: "Key", value: (e) => e.key },
        { key: "size", label: "Size", value: (e) => e.size, render: (e) => humanSize(e.size), align: "end" },
        { key: "created", label: "Saved", value: (e) => ms(e.created_at), render: (e) => ago(e.created_at), text: (e) => e.created_at },
        { key: "used", label: "Last used", value: (e) => ms(e.used_at), render: (e) => ago(e.used_at), text: (e) => e.used_at },
        { key: "expires", label: "Deleted", value: (e) => ms(e.expires_at), render: (e) => untilNode(e.expires_at), text: (e) => e.expires_at },
        { key: "reads", label: "Restores", value: (e) => e.reads, align: "end" }
    ];
    el.facets = [{ key: "repo", label: "repository", of: (e) => e.repo }];
    el.rowId = (e) => e.repo + "|" + e.ref + "|" + e.key;
    el.sort = { key: "used", dir: "desc" };
    el.rows = d.entries;
};

// --- GitHub credential ---

pages.github = function (): void {
    setTitle("GitHub");
    renderSidebar("github");
    apiFetch<GitHubCredentialData>("/github-credential").then(function (d) {
        var source = d.source === "app" ? "GitHub App" : d.source === "token" ? "token" : "none";
        var ss = d.secret_server;
        var html = "<h1>GitHub credential</h1>";
        html += '<div class="stat-grid"><div class="stat-card"><div class="stat-value">' + h(source) + '</div><div class="stat-label">In use</div></div></div>';
        html += '<div class="card"><p>buildhost authenticates its own GitHub calls with this credential: run status for run files, default branches, collaborator checks. Every call goes through <code>' + h(d.github_api) + "</code>, which refuses a call with no token.</p>";
        if (d.env_credential) {
            html += "<p>The credential comes from the environment (<code>BUILDHOST_GITHUB_TOKEN</code> or <code>BUILDHOST_GITHUB_APP_ID</code>), which takes precedence over secret-server.</p>";
        } else if (!ss.configured) {
            html += '<p class="error">No <code>BUILDHOST_SECRET_SERVER_TOKEN</code> is set, so every GitHub call fails: run files never learn that their run ended. Issue a secret-server machine token entitled to <code>BUILDHOST_GITHUB_TOKEN</code> (or <code>BUILDHOST_GITHUB_APP_ID</code> and <code>BUILDHOST_GITHUB_APP_PRIVATE_KEY</code>) and save it on the <a href="#/settings">Settings</a> page.</p>';
        } else if (ss.ok) {
            html += "<p>secret-server vended the " + h(ss.applied === "app" ? "GitHub App" : "token") + " at " + h(ss.at || "") + ". buildhost fetches it again every 15 minutes.</p>";
        } else {
            html += '<p class="error">The last secret-server fetch failed at ' + h(ss.at || "") + ": " + h(ss.error || "") + ". buildhost retries every minute.</p>";
        }
        html += "</div>";
        document.getElementById("content")!.innerHTML = html;
    });
};

pages.settings = function (): void {
    setTitle("Settings");
    renderSidebar("settings");
    apiFetch<SettingsData>("/settings").then(renderSettings);
};

const SETTING_SOURCE_BADGE: Record<string, string> = { dashboard: "badge-info", environment: "badge-neutral", default: "badge-neutral" };

const renderSettings = function (d: SettingsData): void {
    const pending = d.settings.filter((s) => s.restart_pending);
    let html = "<h1>Settings</h1>";
    html += '<div class="card"><p class="muted">A value saved here takes precedence over the environment variable of the same name. Clear it to fall back to the environment, then the default. ' +
        "Upload limits apply at once. Every other setting applies when the server restarts. A change after which the server would not start is refused.</p>";
    if (pending.length) {
        html += '<p class="error">' + h(pending.length) + " saved setting" + (pending.length === 1 ? " waits" : "s wait") + " for a restart: " +
            pending.map((s) => "<code>" + h(s.name) + "</code>").join(", ") + ".</p>";
    }
    if (d.can_restart) {
        html += '<p><button class="btn btn-danger" id="settings-restart">Restart server</button> <span class="muted">Finishes in-flight requests, then exits so the container restart policy starts it again.</span></p>';
    }
    html += "</div>";
    const groups: string[] = [];
    for (const s of d.settings) if (groups.indexOf(s.group) < 0) groups.push(s.group);
    for (const g of groups) {
        html += '<div class="card"><h2>' + h(g) + '</h2><table class="data-table settings-table"><tbody>';
        for (const s of d.settings.filter((x) => x.group === g)) html += settingRowHTML(s);
        html += "</tbody></table></div>";
    }
    document.getElementById("content")!.innerHTML = html;
    document.querySelectorAll<HTMLFormElement>("form[data-setting]").forEach(function (f) {
        f.addEventListener("submit", function (e) {
            e.preventDefault();
            const input = f.querySelector("input") as HTMLInputElement;
            saveSetting(f.dataset.setting!, "PUT", input.value);
        });
    });
    document.querySelectorAll<HTMLButtonElement>("button[data-clear]").forEach(function (b) {
        b.addEventListener("click", function () { saveSetting(b.dataset.clear!, "DELETE", null); });
    });
    document.getElementById("settings-restart")?.addEventListener("click", restartServer);
};

const settingRowHTML = function (s: SettingRow): string {
    const secret = s.kind === "secret";
    let html = '<tr><td class="settings-name"><code>' + h(s.name) + '</code> <span class="badge ' + (SETTING_SOURCE_BADGE[s.source] ?? "badge-neutral") + '">' + h(s.source) + "</span>";
    if (s.restart_pending) html += ' <span class="badge badge-warning">restart pending</span>';
    html += '<div class="muted">' + h(s.description) + (s.default ? " Default: <code>" + h(s.default) + "</code>." : "") + "</div></td><td>";
    if (s.env_only) {
        html += "<code>" + h(s.value || "(empty)") + '</code><div class="muted">Environment only.</div>';
    } else {
        const placeholder = secret ? (s.set ? "set (hidden)" : "not set") : s.default;
        html += '<form class="settings-form" data-setting="' + h(s.name) + '"><input class="form-input" type="' + (secret ? "password" : "text") +
            '" autocomplete="off" value="' + h(secret ? "" : s.value) + '" placeholder="' + h(placeholder) + '">' +
            '<button class="btn btn-primary btn-sm" type="submit">Save</button>' +
            (s.source === "dashboard" ? '<button class="btn btn-sm" type="button" data-clear="' + h(s.name) + '">Clear</button>' : "") + "</form>";
    }
    return html + "</td></tr>";
};

const saveSetting = function (name: string, method: "PUT" | "DELETE", value: string | null): void {
    fetch("/api/settings/" + encodeURIComponent(name), {
        method: method,
        headers: { "Content-Type": "application/json" },
        body: value === null ? undefined : JSON.stringify({ value: value })
    }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Not saved: " + t); });
        return res.json().then(renderSettings);
    }).catch(function () { alert("Could not save (preview/demo mode has no backend)."); });
};

const restartServer = function (): void {
    if (!confirm("Restart buildhost? It finishes in-flight requests first.")) return;
    fetch("/api/restart", { method: "POST" }).then(function (res) {
        if (!res.ok) return res.text().then(function (t) { alert("Not restarted: " + t); });
        alert("The server is restarting. Reload this page in a moment.");
    }).catch(function () { alert("Could not restart (preview/demo mode has no backend)."); });
};

pages.mirror = function (): void {
    setTitle("Registry Mirror");
    renderSidebar("mirror");
    apiFetch<MirrorData>("/mirror").then(renderMirror);
};

const renderMirror = function (d: MirrorData): void {
    let manifests = 0;
    let reads = 0;
    for (const b of d.blobs) {
        if (b.media_type) manifests++;
        reads += b.reads;
    }
    const keepDays = Math.round(d.keep_seconds / 86400);
    const hosts = d.upstreams.map((u) => "<code>" + h(u.service) + ".</code> fronts " + h(u.remote)).join(", ");
    let html = "<h1>Registry Mirror</h1>";
    html += '<p class="rf-lede">A read-only pull-through mirror: ' + hosts + ". A pull needs a token with the read scope. " +
        "A tag is asked of its upstream again after " + h(d.tag_ttl_seconds) + "s, and the last digest still serves while the upstream is down. " +
        "A blob nothing reads for " + h(keepDays) + "d goes, and the least recently used go sooner while free disk is under " + h(d.min_free_percent) + "%.</p>";
    html += '<div class="rf-kpis">' +
        kpi(d.blobs.length - manifests, "blobs") + kpi(manifests, "manifests") + kpi(d.tags.length, "tags") + kpi(humanSize(d.total_bytes), "stored") + kpi(reads, "reads") +
        "</div>";
    html += mirrorJanitorLine(d);
    html += '<section class="rf-panel"><h2>Disk</h2>' +
        (d.disk_total ? '<bar-chart id="mirror-disk"></bar-chart>' : '<p class="empty">The data directory reports no filesystem size.</p>') + "</section>";
    html += d.tags.length
        ? '<section class="rf-panel"><h2>Tags</h2><data-table id="mirror-tags"></data-table></section>'
        : '<section class="rf-panel"><p class="empty">No tag was pulled through the mirror.</p></section>';
    html += d.blobs.length
        ? '<section class="rf-panel"><h2>Stored</h2><data-table id="mirror-blobs"></data-table></section>'
        : '<section class="rf-panel"><p class="empty">The mirror stores nothing.</p></section>';
    document.getElementById("content")!.innerHTML = html;
    fillDiskChart("mirror-disk", "mirror", d);
    fillMirrorTables(d);
};

const mirrorJanitorLine = function (d: MirrorData): string {
    const j = d.janitor;
    const every = "sweeps every " + Math.round(d.check_interval_seconds / 60) + "m";
    if (!j) return '<p class="rf-janitor">Janitor: no sweep yet since this server started (' + every + ").</p>";
    if (j.deferred) return '<p class="rf-janitor">Janitor: last sweep ' + h(timeAgo(j.at)) + " was deferred, writes in flight (" + every + ").</p>";
    let s = "Janitor: last sweep " + h(timeAgo(j.at)) + " in " + h(j.duration_ms) + "ms over " + h(j.blobs) + " blobs, expired " + h(j.expired) +
        (j.pressure_deleted ? ", deleted " + h(j.pressure_deleted) + " for disk pressure" : "") + ", freed " + h(humanSize(j.freed_bytes)) + " &middot; " + every;
    if (j.error) s += ' &middot; <span class="rf-bad">sweep failed: ' + h(j.error) + "</span>";
    return '<p class="rf-janitor">' + s + "</p>";
};

const fillMirrorTables = function (d: MirrorData): void {
    const ago = function (iso: string): Node {
        const span = document.createElement("span");
        span.textContent = shortAgo(iso);
        span.title = formatTime(iso);
        return span;
    };
    const short = (digest: string): string => digest.slice(0, 19);
    const tags = document.getElementById("mirror-tags") as DataTableElement<MirrorTagRow> | null;
    if (tags) {
        tags.columns = [
            { key: "upstream", label: "Upstream", value: (t) => t.upstream },
            { key: "image", label: "Image", value: (t) => t.repository + ":" + t.tag },
            { key: "digest", label: "Digest", value: (t) => t.digest, render: (t) => short(t.digest) },
            { key: "type", label: "Type", value: (t) => t.media_type },
            { key: "checked", label: "Checked", value: (t) => ms(t.checked_at), render: (t) => ago(t.checked_at), text: (t) => t.checked_at }
        ];
        tags.facets = [{ key: "upstream", label: "upstream", of: (t) => t.upstream }];
        tags.rowId = (t) => t.upstream + "|" + t.repository + "|" + t.tag;
        tags.sort = { key: "checked", dir: "desc" };
        tags.rows = d.tags;
    }
    const blobs = document.getElementById("mirror-blobs") as DataTableElement<MirrorBlobRow> | null;
    if (blobs) {
        blobs.columns = [
            { key: "upstream", label: "Upstream", value: (b) => b.upstream },
            { key: "digest", label: "Digest", value: (b) => b.digest, render: (b) => short(b.digest) },
            { key: "kind", label: "Kind", value: (b) => (b.media_type ? "manifest" : "blob") },
            { key: "size", label: "Size", value: (b) => b.size, render: (b) => humanSize(b.size), align: "end" },
            { key: "created", label: "Fetched", value: (b) => ms(b.created_at), render: (b) => ago(b.created_at), text: (b) => b.created_at },
            { key: "used", label: "Last read", value: (b) => ms(b.used_at), render: (b) => ago(b.used_at), text: (b) => b.used_at },
            { key: "expires", label: "Deleted", value: (b) => ms(b.expires_at), render: (b) => untilNode(b.expires_at), text: (b) => b.expires_at },
            { key: "reads", label: "Reads", value: (b) => b.reads, align: "end" }
        ];
        blobs.facets = [
            { key: "upstream", label: "upstream", of: (b) => b.upstream },
            { key: "kind", label: "kind", of: (b) => (b.media_type ? "manifest" : "blob") }
        ];
        blobs.rowId = (b) => b.upstream + "|" + b.digest;
        blobs.sort = { key: "used", dir: "desc" };
        blobs.rows = d.blobs;
    }
};

pages.duplicates = function (): void {
    setTitle("Duplicates");
    renderSidebar("duplicates");
    apiFetch<DuplicatesData>("/duplicates").then(function (d) {
        var html = "<h1>Duplicates</h1>";
        if (!d.groups || !d.groups.length) {
            html += '<div class="card"><p class="empty">No project is split across roots. Every GitHub repo maps to one namespace.</p></div>';
            document.getElementById("content")!.innerHTML = html;
            return;
        }
        html += "<p>These projects share a GitHub repo id but sit under different roots. " +
            "A rename made each of them: the release history stayed under the old name and new publishes landed under the new one.</p>";
        for (var g = 0; g < d.groups.length; g++) {
            var grp = d.groups[g];
            html += '<div class="card"><h2>' + h(grp.repo || grp.repo_id) + "</h2>";
            html += '<table class="table"><thead><tr><th>Project</th><th>Root</th><th>Releases</th><th>Merge into</th></tr></thead><tbody>';
            for (var p = 0; p < grp.projects.length; p++) {
                var pr = grp.projects[p];
                var opts = "";
                for (var q = 0; q < grp.projects.length; q++) {
                    if (grp.projects[q].name === pr.name) continue;
                    opts += '<button class="btn" onclick="App.previewMerge(\'' + h(pr.name) + "', '" +
                        h(grp.projects[q].name) + '\')">' + h(grp.projects[q].name) + "</button> ";
                }
                html += "<tr><td><code>" + h(pr.name) + "</code></td><td>" + h(pr.root) + "</td><td>" +
                    pr.releases + "</td><td>" + opts + "</td></tr>";
            }
            html += "</tbody></table></div>";
        }
        html += '<div class="card" id="merge-plan"><p class="empty">Pick a merge target above to preview exactly what moves.</p></div>';
        document.getElementById("content")!.innerHTML = html;
    });
};

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

// A renderer runs inside .then(), so its failure surfaces here, not at the
// router's try.
window.addEventListener("unhandledrejection", function (ev) {
    fail(ev.reason);
});

// Exported == reachable as App.x from the inline onclick handlers above.
export { applyMerge, checkDeletedBranches, copyInventory, copyTempLink, copyText, deleteToken, downloadArtifact, downloadBackup, downloadInventory, editToken, pages, previewMerge, recheckGoproxy, reloadTokens, runDeletedBranches, runRetention, saveToken, setVersioning, setVisibility };
