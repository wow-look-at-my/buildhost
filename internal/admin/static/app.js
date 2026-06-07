"use strict";

var App = {};

App.demo = false;
App.sidebarCache = null;

App.h = function (s) {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
};

App.humanSize = function (b) {
    if (b < 1024) return b + " B";
    var units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
    var i = -1;
    var v = b;
    do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
    return v.toFixed(1) + " " + units[i];
};

App.timeAgo = function (s) {
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

App.formatTime = function (s) {
    if (!s) return "-";
    var d = new Date(s);
    if (isNaN(d.getTime())) return "-";
    var pad = function (n) { return n < 10 ? "0" + n : "" + n; };
    return d.getUTCFullYear() + "-" + pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate()) +
        " " + pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()) + " UTC";
};

App.fetch = function (path) {
    if (App.demo) return Promise.resolve(App.demoData[path] || {});
    return fetch("/api" + path).then(function (r) {
        if (!r.ok) throw new Error(r.status);
        return r.json();
    }).catch(function () {
        App.demo = true;
        return App.demoData[path] || {};
    });
};

App.setTitle = function (t) {
    document.title = t + " - Buildhost Admin";
};

var NAV_ITEMS = [
    { id: "dashboard", href: "#/", label: "Dashboard", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M10.707 2.293a1 1 0 00-1.414 0l-7 7a1 1 0 001.414 1.414L4 10.414V17a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 011-1h2a1 1 0 011 1v2a1 1 0 001 1h2a1 1 0 001-1v-6.586l.293.293a1 1 0 001.414-1.414l-7-7z"/></svg>' },
    { id: "projects", href: "#/projects", label: "Projects", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>' },
    { id: "registries", href: "#/registries", label: "Registries", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4 4a2 2 0 012-2h8a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V4zm2 0h8v3H6V4zm0 5h8v2H6V9zm0 4h5v2H6v-2z" clip-rule="evenodd"/></svg>' },
    { id: "tokens", href: "#/tokens", label: "Tokens", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M18 8a6 6 0 01-7.743 5.743L10 14l-1 1-1 1H6v2H2v-4l4.257-4.257A6 6 0 1118 8zm-6-4a1 1 0 100 2 2 2 0 012 2 1 1 0 102 0 4 4 0 00-4-4z" clip-rule="evenodd"/></svg>' },
    { id: "sites", href: "#/sites", label: "Sites", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4.083 9h1.946c.089-1.546.383-2.97.837-4.118A6.004 6.004 0 004.083 9zM10 2a8 8 0 100 16 8 8 0 000-16zm0 2c-.076 0-.232.032-.465.262-.238.234-.497.623-.737 1.182-.389.907-.673 2.142-.766 3.556h3.936c-.093-1.414-.377-2.649-.766-3.556-.24-.56-.5-.948-.737-1.182C10.232 4.032 10.076 4 10 4zm3.971 5c-.089-1.546-.383-2.97-.837-4.118A6.004 6.004 0 0115.917 9h-1.946zm-2.003 2H8.032c.093 1.414.377 2.649.766 3.556.24.56.5.948.737 1.182.233.23.389.262.465.262.076 0 .232-.032.465-.262.238-.234.497-.623.737-1.182.389-.907.673-2.142.766-3.556zm1.166 4.118c.454-1.147.748-2.572.837-4.118h1.946a6.004 6.004 0 01-2.783 4.118zm-6.268 0C6.412 13.97 6.118 12.546 6.029 11H4.083a6.004 6.004 0 002.783 4.118z" clip-rule="evenodd"/></svg>' },
    { id: "oidc", href: "#/oidc", label: "OIDC Policies", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>' }
];

App.renderSidebar = function (nav) {
    var sb = App.sidebarCache || {};
    var build = sb.build || {};
    var links = "";
    for (var i = 0; i < NAV_ITEMS.length; i++) {
        var n = NAV_ITEMS[i];
        links += '<li><a href="' + n.href + '"' + (n.id === nav ? ' class="active"' : '') + '>' + n.icon + " " + App.h(n.label) + "</a></li>";
    }
    var footer = "";
    if (build.commit_url) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <a href="' + App.h(build.commit_url) + '" class="sidebar-info-link">' + App.h(build.short_commit) + "</a></div>";
    } else if (build.commit) {
        footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Commit</span> <span>' + App.h(build.short_commit) + "</span></div>";
    }
    if (sb.build_age) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Built</span> <span>' + App.h(sb.build_age) + "</span></div>";
    if (sb.cpu_percent) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">CPU</span> <span>' + App.h(sb.cpu_percent) + "</span></div>";
    if (sb.disk_total) footer += '<div class="sidebar-info-row"><span class="sidebar-info-label">Disk</span> <span>' + App.h(sb.disk_used) + " / " + App.h(sb.disk_total) + "</span></div>";

    document.getElementById("sidebar").innerHTML =
        '<div class="sidebar-header"><div class="logo">B</div><div><div class="sidebar-title">Buildhost</div><div class="sidebar-subtitle">Admin Dashboard</div></div></div>' +
        '<ul class="nav-list">' + links + "</ul>" +
        '<div class="sidebar-footer">' + footer + "</div>";
};

App.badge = function (type, text) { return '<span class="badge badge-' + type + '">' + App.h(text) + "</span>"; };

// urlTpl renders a copyable URL with inline os/arch dropdowns. `base` is the
// text before the os dropdown, `mid` the text between the os and arch dropdowns
// (e.g. "&arch=" for the query-param download URLs), and `suffix` optional text
// after. `tpl` is the full template string with {os}/{arch} placeholders that
// the copy button substitutes the selected values into.
App.urlTpl = function (tpl, base, mid, suffix) {
    return '<span class="url-tpl" data-tpl="' + App.h(tpl) + '">' +
        "<code>" + App.h(base) + "</code>" +
        '<select class="tpl-select" data-var="os"><option value="linux">linux</option><option value="darwin">darwin</option><option value="windows">windows</option><option value="freebsd">freebsd</option></select>' +
        "<code>" + App.h(mid) + "</code>" +
        '<select class="tpl-select" data-var="arch"><option value="amd64">amd64</option><option value="arm64">arm64</option><option value="386">386</option><option value="arm">arm</option></select>' +
        (suffix ? "<code>" + App.h(suffix) + "</code>" : "") +
        "</span><copy-btn></copy-btn>";
};

App.codeBlock = function (label, code) {
    return '<div class="code-block"><div class="code-label">' + App.h(label) +
        '<copy-btn class="code-copy-btn" data-src="pre"></copy-btn></div><pre>' + App.h(code) + "</pre></div>";
};

// --- Pages ---

App.pages = {};

App.pages.dashboard = function () {
    App.setTitle("Dashboard");
    App.renderSidebar("dashboard");
    App.fetch("/dashboard").then(function (d) {
        var s = d.stats || {};
        var b = d.build || {};
        var cfg = d.config || {};
        var html = '<h1>Dashboard</h1><div class="stat-grid">';
        var cards = [
            [s.project_count, "Projects", "#/projects"],
            [s.release_count, "Releases", "#/projects"],
            [s.artifact_count, "Artifacts", "#/artifacts"],
            [App.humanSize(s.total_storage_bytes || 0), "Storage Used", "#/storage"],
            [s.token_count, "API Tokens", "#/tokens"],
            [s.site_count || 0, "Sites", "#/sites"]
        ];
        for (var i = 0; i < cards.length; i++) html += '<a href="' + cards[i][2] + '" class="stat-card stat-card-link"><div class="stat-value">' + App.h(cards[i][0]) + '</div><div class="stat-label">' + cards[i][1] + "</div></a>";
        html += "</div>";

        html += '<div class="card"><h2>Server Status</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Version</td><td>" + App.h(b.version) + "</td></tr>";
        if (b.commit_url) html += "<tr><td class='info-label'>Commit</td><td><a href='" + App.h(b.commit_url) + "'><code class='commit'>" + App.h(b.short_commit) + "</code></a></td></tr>";
        else html += "<tr><td class='info-label'>Commit</td><td><code>" + App.h(b.commit) + "</code></td></tr>";
        html += "<tr><td class='info-label'>Built</td><td>" + App.h(b.date || "-") + "</td></tr>";
        html += "<tr><td class='info-label'>Uptime</td><td>" + App.h(d.uptime) + "</td></tr>";
        html += "<tr><td class='info-label'>CPU Usage</td><td>" + App.h(d.cpu_percent) + "</td></tr>";
        html += "<tr><td class='info-label'>CPU Time</td><td>" + App.h(d.cpu_total) + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Configuration</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Base URL</td><td>" + App.h(cfg.base_url) + "</td></tr>";
        html += "<tr><td class='info-label'>API Listen</td><td>" + App.h(cfg.listen_addr) + "</td></tr>";
        html += "<tr><td class='info-label'>Admin Listen</td><td>" + App.h(cfg.admin_listen_addr) + "</td></tr>";
        html += "<tr><td class='info-label'>Data Directory</td><td>" + App.h(cfg.data_dir) + "</td></tr>";
        var issuers = (cfg.oidc_issuers || []).map(function (v) { return "<code>" + App.h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Trusted OIDC Issuers</td><td>" + (issuers || '<span class="empty">None</span>') + "</td></tr>";
        var orgs = (cfg.oidc_orgs || []).map(function (v) { return "<code>" + App.h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Orgs</td><td>" + (orgs || '<span class="empty">None</span>') + "</td></tr>";
        var events = (cfg.oidc_events || []).map(function (v) { return "<code>" + App.h(v) + "</code>"; }).join(", ");
        html += "<tr><td class='info-label'>Allowed OIDC Events</td><td>" + events + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Recent Releases</h2><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Branch</th><th>Status</th><th>Created</th></tr></thead><tbody>';
        var recent = d.recent || [];
        if (recent.length === 0) {
            html += '<tr><td colspan="5" class="empty">No releases yet</td></tr>';
        } else {
            for (var j = 0; j < recent.length; j++) {
                var rel = recent[j];
                html += "<tr><td><a href='#/projects/" + App.h(rel.project_name) + "'>" + App.h(rel.project_name) + "</a></td>";
                html += "<td><a href='#/projects/" + App.h(rel.project_name) + "/releases/" + App.h(rel.version) + "'><code>" + App.h(rel.version) + "</code></a></td>";
                html += "<td>" + (rel.git_branch ? "<code>" + App.h(rel.git_branch) + "</code>" : "-") + "</td>";
                html += "<td>" + (rel.published ? App.badge("success", "Published") : App.badge("warning", "Draft")) + "</td>";
                html += '<td title="' + App.h(App.formatTime(rel.created_at)) + '">' + App.h(App.timeAgo(rel.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;
    });
};

App.pages.projects = function () {
    App.setTitle("Projects");
    App.renderSidebar("projects");
    App.fetch("/projects").then(function (projects) {
        var html = '<h1>Projects</h1><div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Description</th><th>Versioning</th><th>Visibility</th><th>Releases</th><th>Artifacts</th><th>Created</th></tr></thead><tbody>';
        if (projects.length === 0) {
            html += '<tr><td colspan="7" class="empty">No projects yet</td></tr>';
        } else {
            for (var i = 0; i < projects.length; i++) {
                var p = projects[i];
                html += "<tr><td><a href='#/projects/" + App.h(p.name) + "'>" + App.h(p.name) + "</a></td>";
                html += '<td class="truncate">' + App.h(p.description) + "</td>";
                html += "<td>" + App.badge("neutral", p.versioning) + "</td>";
                html += "<td>" + (p.is_private ? App.badge("warning", "Private") : App.badge("success", "Public")) + "</td>";
                html += "<td>" + p.release_count + "</td><td>" + p.artifact_count + "</td>";
                html += '<td title="' + App.h(App.formatTime(p.created_at)) + '">' + App.h(App.timeAgo(p.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;
    });
};

App.pages.project = function (name) {
    App.setTitle(name);
    App.renderSidebar("projects");
    App.fetch("/projects/" + name).then(function (d) {
        var p = d.project;
        var html = "<h1>" + App.h(p.name) + "</h1>";
        html += '<div class="card"><h2>Project Info</h2><table class="info-table">';
        html += "<tr><td class='info-label'>ID</td><td>" + p.id + "</td></tr>";
        if (p.description) html += "<tr><td class='info-label'>Description</td><td>" + App.h(p.description) + "</td></tr>";
        if (p.homepage) html += "<tr><td class='info-label'>Homepage</td><td>" + App.h(p.homepage) + "</td></tr>";
        if (p.license) html += "<tr><td class='info-label'>License</td><td>" + App.h(p.license) + "</td></tr>";
        html += "<tr><td class='info-label'>Versioning</td><td>" + App.badge("neutral", p.versioning) + "</td></tr>";
        html += "<tr><td class='info-label'>Visibility</td><td>" + (p.is_private ? App.badge("warning", "Private") : App.badge("success", "Public")) + "</td></tr>";
        html += '<tr><td class="info-label">Created</td><td title="' + App.h(App.formatTime(p.created_at)) + '">' + App.h(App.timeAgo(p.created_at)) + "</td></tr>";
        html += '<tr><td class="info-label">Updated</td><td title="' + App.h(App.formatTime(p.updated_at)) + '">' + App.h(App.timeAgo(p.updated_at)) + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Releases</h2><table class="data-table"><thead><tr><th>Version</th><th>Branch</th><th>Commit</th><th>Status</th><th>Artifacts</th><th>Published</th><th>Created</th></tr></thead><tbody>';
        var rels = d.releases || [];
        if (rels.length === 0) {
            html += '<tr><td colspan="7" class="empty">No releases yet</td></tr>';
        } else {
            for (var i = 0; i < rels.length; i++) {
                var r = rels[i];
                html += "<tr><td><a href='#/projects/" + App.h(p.name) + "/releases/" + App.h(r.version) + "'><code>" + App.h(r.version) + "</code></a></td>";
                html += "<td>" + (r.git_branch ? "<code>" + App.h(r.git_branch) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.git_commit ? '<code class="commit">' + App.h(r.git_commit) + "</code>" : "-") + "</td>";
                html += "<td>" + (r.published ? App.badge("success", "Published") : App.badge("warning", "Draft")) + "</td>";
                html += "<td>" + r.artifact_count + "</td>";
                html += "<td>" + App.h(App.formatTime(r.published_at)) + "</td>";
                html += '<td title="' + App.h(App.formatTime(r.created_at)) + '">' + App.h(App.timeAgo(r.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        var sites = d.sites || [];
        if (sites.length > 0) {
            var sitesBase = (d.services || {}).sites || "";
            html += '<div class="card"><h2>Sites</h2><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
            for (var k = 0; k < sites.length; k++) {
                var si = sites[k];
                html += "<tr><td><code>" + App.h(si.branch) + "</code></td>";
                html += "<td>" + si.file_count + "</td>";
                html += "<td>" + App.h(App.humanSize(si.size)) + "</td>";
                html += "<td>" + (si.git_commit ? '<code class="commit">' + App.h(si.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + App.h(App.formatTime(si.updated_at)) + '">' + App.h(App.timeAgo(si.updated_at)) + "</td>";
                html += '<td><a href="' + App.h(sitesBase + "/" + p.name + "/branch/" + si.branch + "/") + '" target="_blank">Open</a></td></tr>';
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content").innerHTML = html;
    });
};

App.pages.release = function (name, version) {
    App.setTitle(name + " " + version);
    App.renderSidebar("projects");
    App.fetch("/projects/" + name + "/releases/" + version).then(function (d) {
        var p = d.project, r = d.release, bu = d.base_url, svc = d.services || {};
        var dlBase = (svc.dl || "") + "/" + p.name;
        var html = "<h1><a href='#/projects/" + App.h(p.name) + "'>" + App.h(p.name) + "</a> / " + App.h(r.version) + "</h1>";

        html += '<div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + (d.artifacts || []).length + '</div><div class="stat-label">Artifacts</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.total_size || 0)) + '</div><div class="stat-label">Total Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + (d.total_downloads || 0) + '</div><div class="stat-label">Downloads</div></div>';
        html += "</div>";

        html += '<div class="card"><h2>Release Info</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Version</td><td><code>" + App.h(r.version) + "</code></td></tr>";
        html += "<tr><td class='info-label'>Status</td><td>" + (r.published ? App.badge("success", "Published") : App.badge("warning", "Draft")) + "</td></tr>";
        if (r.git_branch) html += "<tr><td class='info-label'>Branch</td><td><code>" + App.h(r.git_branch) + "</code></td></tr>";
        if (r.git_commit) html += "<tr><td class='info-label'>Commit</td><td><code>" + App.h(r.git_commit) + "</code></td></tr>";
        if (r.notes) html += "<tr><td class='info-label'>Notes</td><td>" + App.h(r.notes) + "</td></tr>";
        html += "<tr><td class='info-label'>Published At</td><td>" + App.h(App.formatTime(r.published_at)) + "</td></tr>";
        html += "<tr><td class='info-label'>Created At</td><td>" + App.h(App.formatTime(r.created_at)) + "</td></tr>";
        html += "</table></div>";

        html += '<div class="card"><h2>Artifacts</h2><table class="data-table"><thead><tr><th>Platform</th><th>Kind</th><th>Filename</th><th>Size</th><th>Downloads</th><th>Links</th></tr></thead><tbody>';
        var arts = d.artifacts || [];
        if (arts.length === 0) {
            html += '<tr><td colspan="6" class="empty">No artifacts</td></tr>';
        } else {
            for (var i = 0; i < arts.length; i++) {
                var a = arts[i];
                html += "<tr><td>" + App.badge("info", a.os + "/" + a.arch) + "</td>";
                html += "<td>" + App.badge("neutral", a.kind) + "</td>";
                html += "<td>" + (a.filename ? "<code>" + App.h(a.filename) + "</code>" : '<span class="muted">-</span>') + "</td>";
                html += "<td>" + App.h(App.humanSize(a.size)) + "</td>";
                html += "<td>" + a.download_count + "</td>";
                html += '<td class="dl-links">';
                var dlQ = "?v=" + r.version + "&os=" + a.os + "&arch=" + a.arch;
                html += '<a href="' + App.h(dlBase + dlQ) + '" title="Direct download">raw</a>';
                if (a.debug_storage_key) html += ' <a href="' + App.h(dlBase + dlQ + "&debug=1") + '" title="Debug symbols">debug</a>';
                var pkgs = a.packages || [];
                for (var j = 0; j < pkgs.length; j++) {
                    html += ' <a href="' + App.h(dlBase + dlQ + "&fmt=" + pkgs[j].format) + '" title="' + App.h(pkgs[j].filename) + " (" + App.h(App.humanSize(pkgs[j].size)) + ')">' + App.h(pkgs[j].format) + "</a>";
                }
                html += "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        var aptU = (svc.apt || "") + "/" + p.name;
        var brewU = (svc.brew || "") + "/" + p.name;
        var npmU = (svc.npm || "") + "/@buildhost/" + p.name;
        var ociU = (svc.oci || "") + "/v2/" + p.name + "/manifests/" + r.version;
        html += '<div class="card"><h2>Download Endpoints</h2><table class="info-table">';
        html += "<tr><td class='info-label'>Direct (latest)</td><td class='endpoint-cell'>" + App.urlTpl(dlBase + "?os={os}&arch={arch}", dlBase + "?os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>Direct (version)</td><td class='endpoint-cell'>" + App.urlTpl(dlBase + "?v=" + r.version + "&os={os}&arch={arch}", dlBase + "?v=" + r.version + "&os=", "&arch=") + "</td></tr>";
        if (r.git_branch) html += "<tr><td class='info-label'>Direct (branch)</td><td class='endpoint-cell'>" + App.urlTpl(dlBase + "?branch=" + r.git_branch + "&os={os}&arch={arch}", dlBase + "?branch=" + r.git_branch + "&os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>APT</td><td class='endpoint-cell'><a href='" + App.h(aptU + "/dists/stable/Release") + "' data-copy='" + App.h(aptU) + "'>" + App.h(aptU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Homebrew</td><td class='endpoint-cell'><a href='" + App.h(brewU) + "'>" + App.h(brewU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>npm</td><td class='endpoint-cell'><a href='" + App.h(npmU) + "'>" + App.h(npmU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>OCI</td><td class='endpoint-cell'><a href='" + App.h(ociU) + "'>" + App.h(ociU) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "</table></div>";

        document.getElementById("content").innerHTML = html;
    });
};

App.pages.registries = function () {
    App.setTitle("Registries");
    App.renderSidebar("registries");
    App.fetch("/registries").then(function (d) {
        var bu = d.base_url, svc = d.services || {};
        var dl = svc.dl || "", apt = svc.apt || "", brew = svc.brew || "", npm = svc.npm || "", oci = svc.oci || "", sites = svc.sites || "";
        var npmHost = npm.replace(/^https?:\/\//, ""), ociHost = oci.replace(/^https?:\/\//, "");
        var html = "<h1>Registry Endpoints</h1>";

        html += '<div class="card"><h2>Direct Downloads</h2><p class="section-desc">Download artifacts directly by platform. OS and architecture are query parameters; version, latest, and branch resolution too.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Latest</td><td class='endpoint-cell'>" + App.urlTpl(dl + "/{project}?os={os}&arch={arch}", dl + "/{project}?os=", "&arch=") + "</td></tr>";
        html += "<tr><td class='info-label'>Version</td><td class='endpoint-cell'><code>" + App.h(dl + "/{project}?v={version}&os={os}&arch={arch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Branch</td><td class='endpoint-cell'><code>" + App.h(dl + "/{project}?branch={branch}&os={os}&arch={arch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("curl", 'curl -fsSL -H "Authorization: Bearer $TOKEN" \\\n  "' + dl + '/{project}?os=linux&arch=amd64" -o {project}');
        html += App.codeBlock("Query parameters", "?os=linux         # Required: target OS\n?arch=amd64       # Required: target architecture\n?v={version}      # Pin to a version (default: latest)\n?branch={branch}  # Latest build on a git branch\n?fmt=tar.gz       # Repackage: raw, tar.gz, tar.xz, tar.zst, zip\n?debug=1          # Debug symbols");
        html += "</div>";

        html += '<div class="card"><h2>APT Repository</h2><p class="section-desc">Debian/Ubuntu package repository. Packages are generated on demand at download time.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Release</td><td class='endpoint-cell'><code>" + App.h(apt + "/{project}/dists/stable/Release") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>InRelease</td><td class='endpoint-cell'><code>" + App.h(apt + "/{project}/dists/stable/InRelease") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Packages</td><td class='endpoint-cell'><code>" + App.h(apt + "/{project}/dists/stable/main/binary-{arch}/Packages") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Pool</td><td class='endpoint-cell'><code>" + App.h(apt + "/{project}/pool/{filename}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("Setup (public project)", 'echo "deb [trusted=yes] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\nsudo apt update && sudo apt install {project}');
        html += App.codeBlock("Setup (private project)", 'echo "deb [trusted=yes] ' + apt + '/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/{project}.conf\nmachine ' + apt + "/{project}\n  login token\n  password $TOKEN\nEOF\nsudo apt update && sudo apt install {project}");
        html += "</div>";

        html += '<div class="card"><h2>Homebrew Tap</h2><p class="section-desc">Homebrew formula served as a single Ruby file. Auto-detects macOS and Linux bottles.</p>';
        html += '<table class="info-table"><tr><td class="info-label">Formula</td><td class="endpoint-cell"><code>' + App.h(brew + "/{project}") + "</code><copy-btn data-src='code'></copy-btn></td></tr></table>";
        html += App.codeBlock("Install (public project)", "brew install " + brew + "/{project}");
        html += App.codeBlock("Install (private project)", "HOMEBREW_BUILDHOST_TOKEN=$TOKEN brew install " + brew + "/{project}");
        html += "</div>";

        html += '<div class="card"><h2>npm Registry</h2><p class="section-desc">npm-compatible registry. Packages are scoped under <code>@buildhost</code>.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Package metadata</td><td class='endpoint-cell'><code>" + App.h(npm + "/@buildhost/{project}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Tarball</td><td class='endpoint-cell'><code>" + App.h(npm + "/@buildhost/{project}/-/{project}-{version}.tgz") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("Setup", "npm config set @buildhost:registry " + npm + "/\nnpm config set //" + npmHost + "/:_authToken $TOKEN   # if private\nnpm install @buildhost/{project}");
        html += "</div>";

        html += '<div class="card"><h2>OCI Distribution (Docker)</h2><p class="section-desc">OCI-compatible registry for pulling artifacts as container images.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>API check</td><td class='endpoint-cell'><a href='" + App.h(oci + "/v2/") + "' data-copy='" + App.h(oci + "/v2/") + "'>" + App.h(oci + "/v2/") + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Manifest</td><td class='endpoint-cell'><code>" + App.h(oci + "/v2/{project}/manifests/{reference}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Blob</td><td class='endpoint-cell'><code>" + App.h(oci + "/v2/{project}/blobs/{digest}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("Docker pull", "docker pull " + ociHost + "/{project}:{version}");
        html += App.codeBlock("Private project", "echo $TOKEN | docker login " + ociHost + " -u token --password-stdin\ndocker pull " + ociHost + "/{project}:{version}");
        html += "</div>";

        html += '<div class="card"><h2>Static Sites</h2><p class="section-desc">Host small, self-contained static sites with independent per-branch deployments.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>Deploy</td><td class='endpoint-cell'><code>PUT " + App.h(sites + "/{project}/branch/{branch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Serve</td><td class='endpoint-cell'><code>" + App.h(sites + "/{project}/branch/{branch}/{path}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Delete</td><td class='endpoint-cell'><code>DELETE " + App.h(sites + "/{project}/branch/{branch}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>List branches</td><td class='endpoint-cell'><code>GET " + App.h(sites + "/{project}/branches") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("Deploy (curl)", 'tar czf - -C ./dist . | curl -X PUT \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/gzip" \\\n  --data-binary @- \\\n  ' + sites + "/{project}/branch/{branch}");
        html += "</div>";

        html += '<div class="card"><h2>REST API</h2><p class="section-desc">JSON API for managing projects, releases, and artifacts programmatically. Served on the main domain.</p>';
        html += '<table class="info-table">';
        html += "<tr><td class='info-label'>List projects</td><td class='endpoint-cell'><a href='" + App.h(bu + "/api/v1/projects") + "'>GET " + App.h(bu + "/api/v1/projects") + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Get project</td><td class='endpoint-cell'><code>GET " + App.h(bu + "/api/v1/projects/{project}") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>List releases</td><td class='endpoint-cell'><code>GET " + App.h(bu + "/api/v1/projects/{project}/releases") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "<tr><td class='info-label'>Publish</td><td class='endpoint-cell'><code>POST " + App.h(bu + "/api/v1/projects/{project}/releases/{version}/publish") + "</code><copy-btn data-src='code'></copy-btn></td></tr>";
        html += "</table>";
        html += App.codeBlock("Authentication", "# Bearer token\ncurl -H \"Authorization: Bearer $TOKEN\" " + bu + "/api/v1/projects\n\n# Basic auth\ncurl -u \"token:$TOKEN\" " + bu + "/api/v1/projects\n\n# Query parameter (for clients that can't set headers)\ncurl \"" + bu + '/api/v1/projects?token=$TOKEN"');
        html += "</div>";

        var projects = d.projects || [];
        if (projects.length > 0) {
            html += '<div class="card"><h2>Projects</h2><p class="section-desc">Quick links to project-specific endpoints.</p>';
            html += '<table class="data-table"><thead><tr><th>Project</th><th>Visibility</th><th>Direct Download</th><th>APT</th><th>Brew</th><th>npm</th></tr></thead><tbody>';
            for (var k = 0; k < projects.length; k++) {
                var pr = projects[k];
                var prDl = dl + "/" + pr.name;
                html += "<tr><td><a href='#/projects/" + App.h(pr.name) + "'>" + App.h(pr.name) + "</a></td>";
                html += "<td>" + (pr.is_private ? App.badge("warning", "Private") : App.badge("success", "Public")) + "</td>";
                html += "<td class='endpoint-cell'><span class='url-tpl' data-tpl='" + App.h(prDl + "?os={os}&arch={arch}") + "'><code class='truncate'>" + App.h(prDl + "?os=") + "</code><select class='tpl-select tpl-select-sm' data-var='os'><option value='linux'>linux</option><option value='darwin'>darwin</option><option value='windows'>windows</option><option value='freebsd'>freebsd</option></select><code>&arch=</code><select class='tpl-select tpl-select-sm' data-var='arch'><option value='amd64'>amd64</option><option value='arm64'>arm64</option><option value='386'>386</option><option value='arm'>arm</option></select></span><copy-btn></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + App.h(apt + "/" + pr.name + "/dists/stable/Release") + "' data-copy='" + App.h(apt + "/" + pr.name) + "'>" + App.h(apt + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + App.h(brew + "/" + pr.name) + "'>" + App.h(brew + "/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "<td class='endpoint-cell'><a href='" + App.h(npm + "/@buildhost/" + pr.name) + "'>" + App.h(npm + "/@buildhost/" + pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
                html += "</tr>";
            }
            html += "</tbody></table></div>";
        }

        document.getElementById("content").innerHTML = html;
    });
};

App.pages.tokens = function () {
    App.setTitle("Tokens");
    App.renderSidebar("tokens");

    function renderTokens(tokens, projects, newToken) {
        var html = '<h1>API Tokens</h1>';

        if (newToken) {
            html += '<div class="token-reveal"><div class="token-reveal-label">New token — copy it now, it won\'t be shown again</div>';
            html += '<div class="token-reveal-value"><code id="new-token-val">' + App.h(newToken) + '</code>';
            html += '<button class="btn btn-sm" onclick="App.copyText(\'new-token-val\')">Copy</button></div></div>';
        }

        var projectOpts = '<option value="">Global</option>';
        for (var pi = 0; pi < projects.length; pi++) {
            projectOpts += '<option value="' + App.h(projects[pi].id) + '">' + App.h(projects[pi].name) + '</option>';
        }

        html += '<div class="card"><h2>Create Token</h2>';
        html += '<form id="create-token-form" class="inline-form">';
        html += '<input class="form-input" type="text" id="tok-name" placeholder="Name" required>';
        html += '<select class="form-select" id="tok-scopes"><option value="read,write">read+write</option><option value="read">read</option><option value="write">write</option></select>';
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
                html += '<td id="tok-row-name-' + t.id + '">' + App.h(t.name) + "</td>";
                html += "<td><code>" + App.h(t.token_prefix) + "...</code></td>";
                html += "<td>" + (t.is_global ? App.badge("neutral", "Global") : App.badge("info", "Project")) + "</td>";
                html += "<td>" + (t.project_name ? "<a href='#/projects/" + App.h(t.project_name) + "'>" + App.h(t.project_name) + "</a>" : "-") + "</td>";
                html += '<td id="tok-row-scopes-' + t.id + '"><code>' + App.h(t.scopes) + "</code></td>";
                html += '<td title="' + App.h(App.formatTime(t.created_at)) + '">' + App.h(App.timeAgo(t.created_at)) + "</td>";
                html += "<td>" + (t.last_used_at ? App.h(App.formatTime(t.last_used_at)) : '<span class="muted">Never</span>') + "</td>";
                var exp = "";
                if (t.expires_at) {
                    if (t.is_expired) exp += App.badge("danger", "Expired") + " ";
                    exp += App.h(App.formatTime(t.expires_at));
                } else {
                    exp = '<span class="muted">Never</span>';
                }
                html += "<td>" + exp + "</td>";
                html += '<td class="row-actions"><button class="btn btn-sm" onclick="App.editToken(' + t.id + ',\'' + App.h(t.name) + '\',\'' + App.h(t.scopes) + '\')">Edit</button> ';
                html += '<button class="btn btn-sm btn-danger" onclick="App.deleteToken(' + t.id + ')">Delete</button></td>';
                html += "</tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;

        var form = document.getElementById("create-token-form");
        if (form) {
            form.addEventListener("submit", function (e) {
                e.preventDefault();
                var name = document.getElementById("tok-name").value.trim();
                var scopes = document.getElementById("tok-scopes").value;
                var projVal = document.getElementById("tok-project").value;
                var body = { name: name, scopes: scopes };
                if (projVal) body.project_id = parseInt(projVal, 10);
                fetch("/api/tokens", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify(body)
                }).then(function (r) {
                    if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
                    return r.json().then(function (d) {
                        App.pages.tokens._reload(d.token);
                    });
                });
            });
        }
    }

    App.pages.tokens._reload = function (newToken) {
        Promise.all([App.fetch("/tokens"), App.fetch("/projects")]).then(function (results) {
            renderTokens(results[0], results[1], newToken || null);
        });
    };

    App.pages.tokens._reload();
};

App.copyText = function (elemId) {
    var el = document.getElementById(elemId);
    if (!el) return;
    navigator.clipboard.writeText(el.textContent || el.innerText);
};

App.editToken = function (id, name, scopes) {
    var nameCell = document.getElementById("tok-row-name-" + id);
    var scopesCell = document.getElementById("tok-row-scopes-" + id);
    var row = document.getElementById("tok-row-" + id);
    if (!nameCell || !scopesCell) return;

    nameCell.innerHTML = '<input class="form-input form-input-sm" type="text" id="edit-name-' + id + '" value="' + App.h(name) + '">';
    scopesCell.innerHTML = '<select class="form-select form-select-sm" id="edit-scopes-' + id + '">' +
        '<option value="read,write"' + (scopes === "read,write" ? " selected" : "") + '>read+write</option>' +
        '<option value="read"' + (scopes === "read" ? " selected" : "") + '>read</option>' +
        '<option value="write"' + (scopes === "write" ? " selected" : "") + '>write</option>' +
        '</select>';
    var actionsCell = row.querySelector(".row-actions");
    if (actionsCell) {
        actionsCell.innerHTML = '<button class="btn btn-sm btn-primary" onclick="App.saveToken(' + id + ')">Save</button> ' +
            '<button class="btn btn-sm" onclick="App.pages.tokens._reload()">Cancel</button>';
    }
};

App.saveToken = function (id) {
    var nameEl = document.getElementById("edit-name-" + id);
    var scopesEl = document.getElementById("edit-scopes-" + id);
    if (!nameEl || !scopesEl) return;
    fetch("/api/tokens/" + id, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: nameEl.value.trim(), scopes: scopesEl.value })
    }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
        App.pages.tokens._reload();
    });
};

App.deleteToken = function (id) {
    if (!confirm("Delete this token? This cannot be undone.")) return;
    fetch("/api/tokens/" + id, { method: "DELETE" }).then(function (r) {
        if (!r.ok) return r.text().then(function (t) { alert("Error: " + t); });
        App.pages.tokens._reload();
    });
};

App.pages.sites = function () {
    App.setTitle("Sites");
    App.renderSidebar("sites");
    App.fetch("/sites").then(function (d) {
        var bu = d.base_url || "", sitesBase = (d.services || {}).sites || "";
        var sites = d.sites || [];

        var byProject = {};
        for (var i = 0; i < sites.length; i++) {
            var s = sites[i];
            if (!byProject[s.project_name]) {
                byProject[s.project_name] = { branches: 0, total_size: 0, total_files: 0, last_updated: s.updated_at };
            }
            var p = byProject[s.project_name];
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
                html += "<tr><td><a href='#/sites/" + App.h(name) + "'>" + App.h(name) + "</a></td>";
                html += "<td>" + info.branches + "</td>";
                html += "<td>" + info.total_files + "</td>";
                html += "<td>" + App.h(App.humanSize(info.total_size)) + "</td>";
                html += '<td title="' + App.h(App.formatTime(info.last_updated)) + '">' + App.h(App.timeAgo(info.last_updated)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";

        html += '<div class="card"><h2>Deploy a Site</h2>';
        html += App.codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project {project} \\\n  --branch {branch} \\\n  --dir ./dist");
        html += App.codeBlock("curl", 'tar czf - -C ./dist . | curl -X PUT \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/gzip" \\\n  --data-binary @- \\\n  ' + sitesBase + "/{project}/branch/{branch}");
        html += "</div>";

        document.getElementById("content").innerHTML = html;
    });
};

App.pages.site = function (name) {
    App.setTitle(name + " - Sites");
    App.renderSidebar("sites");
    App.fetch("/projects/" + name).then(function (d) {
        var p = d.project;
        var sitesBase = (d.services || {}).sites || "";
        var sites = d.sites || [];

        var html = '<h1><a href="#/sites">Sites</a> / ' + App.h(p.name) + "</h1>";

        html += '<div class="card"><table class="data-table"><thead><tr><th>Branch</th><th>Files</th><th>Size</th><th>Commit</th><th>Updated</th><th>Link</th></tr></thead><tbody>';
        if (sites.length === 0) {
            html += '<tr><td colspan="6" class="empty">No branches deployed</td></tr>';
        } else {
            for (var i = 0; i < sites.length; i++) {
                var s = sites[i];
                html += "<tr><td><code>" + App.h(s.branch) + "</code></td>";
                html += "<td>" + s.file_count + "</td>";
                html += "<td>" + App.h(App.humanSize(s.size)) + "</td>";
                html += "<td>" + (s.git_commit ? '<code class="commit">' + App.h(s.git_commit.substring(0, 12)) + "</code>" : "-") + "</td>";
                html += '<td title="' + App.h(App.formatTime(s.updated_at)) + '">' + App.h(App.timeAgo(s.updated_at)) + "</td>";
                html += '<td><a href="' + App.h(sitesBase + "/" + p.name + "/branch/" + s.branch + "/") + '" target="_blank">Open</a></td></tr>';
            }
        }
        html += "</tbody></table></div>";

        html += '<div class="card"><h2>Deploy to ' + App.h(p.name) + "</h2>";
        html += App.codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project " + p.name + " \\\n  --branch {branch} \\\n  --dir ./dist");
        html += App.codeBlock("Delete a branch", 'curl -X DELETE \\\n  -H "Authorization: Bearer $TOKEN" \\\n  ' + bu + "/sites/" + p.name + "/branch/{branch}");
        html += "</div>";

        document.getElementById("content").innerHTML = html;
    });
};

App.pages.oidc = function () {
    App.setTitle("OIDC Policies");
    App.renderSidebar("oidc");
    App.fetch("/oidc").then(function (policies) {
        var html = '<h1>OIDC Policies</h1><div class="card"><table class="data-table"><thead><tr><th>Issuer</th><th>Subject Pattern</th><th>Audience</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th></tr></thead><tbody>';
        if (policies.length === 0) {
            html += '<tr><td colspan="7" class="empty">No OIDC policies configured</td></tr>';
        } else {
            for (var i = 0; i < policies.length; i++) {
                var p = policies[i];
                html += "<tr><td class='truncate'><code>" + App.h(p.issuer) + "</code></td>";
                html += "<td><code>" + App.h(p.subject_pattern) + "</code></td>";
                html += "<td>" + (p.audience ? "<code>" + App.h(p.audience) + "</code>" : '<span class="muted">Any</span>') + "</td>";
                html += "<td>" + (p.project_name ? App.badge("info", "Project") : App.badge("neutral", "Global")) + "</td>";
                html += "<td>" + (p.project_name ? "<a href='#/projects/" + App.h(p.project_name) + "'>" + App.h(p.project_name) + "</a>" : "-") + "</td>";
                html += "<td><code>" + App.h(p.scopes) + "</code></td>";
                html += '<td title="' + App.h(App.formatTime(p.created_at)) + '">' + App.h(App.timeAgo(p.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;
    });
};

App.pages.artifacts = function () {
    App.setTitle("Artifacts");
    App.renderSidebar("dashboard");
    App.fetch("/artifacts").then(function (artifacts) {
        var html = '<h1>All Artifacts</h1><div class="card"><table class="data-table"><thead><tr><th>Project</th><th>Version</th><th>Platform</th><th>Kind</th><th>Filename</th><th>Size</th><th>Downloads</th><th>Created</th></tr></thead><tbody>';
        if (artifacts.length === 0) {
            html += '<tr><td colspan="8" class="empty">No artifacts yet</td></tr>';
        } else {
            for (var i = 0; i < artifacts.length; i++) {
                var a = artifacts[i];
                html += "<tr><td><a href='#/projects/" + App.h(a.project_name) + "'>" + App.h(a.project_name) + "</a></td>";
                html += "<td><a href='#/projects/" + App.h(a.project_name) + "/releases/" + App.h(a.version) + "'><code>" + App.h(a.version) + "</code></a></td>";
                html += "<td>" + App.badge("info", a.os + "/" + a.arch) + "</td>";
                html += "<td>" + App.badge("neutral", a.kind) + "</td>";
                html += "<td>" + (a.filename ? "<code>" + App.h(a.filename) + "</code>" : '<span class="muted">-</span>') + "</td>";
                html += "<td>" + App.h(App.humanSize(a.size)) + "</td>";
                html += "<td>" + a.download_count + "</td>";
                html += '<td title="' + App.h(App.formatTime(a.created_at)) + '">' + App.h(App.timeAgo(a.created_at)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;
    });
};

App.pages.storage = function () {
    App.setTitle("Storage");
    App.renderSidebar("dashboard");
    App.fetch("/storage").then(function (d) {
        var projects = d.projects || [];
        var html = '<h1>Storage Usage</h1><div class="stat-grid">';
        html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.total_bytes || 0)) + '</div><div class="stat-label">Artifact Storage</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.logical_bytes || 0)) + '</div><div class="stat-label">Logical Size</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.physical_bytes || 0)) + '</div><div class="stat-label">Physical Size (dedup)</div></div>';
        html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.disk_bytes || 0)) + '</div><div class="stat-label">Blobs on Disk</div></div>';
        if (d.disk_total) {
            html += '<div class="stat-card"><div class="stat-value">' + App.h(App.humanSize(d.disk_used || 0)) + " / " + App.h(App.humanSize(d.disk_total || 0)) + '</div><div class="stat-label">Filesystem Usage</div></div>';
        }
        html += "</div>";

        var logical = d.logical_bytes || 0;
        var physical = d.physical_bytes || 0;
        var disk = d.disk_bytes || 0;
        var bdRow = function (op, name, bytes, cls) {
            return "<tr" + (cls ? ' class="' + cls + '"' : "") +
                '><td class="bd-op">' + op + '</td><td class="bd-name">' + name +
                '</td><td class="bd-val">' + App.h(App.humanSize(bytes)) + "</td></tr>";
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
                var p = projects[i];
                html += "<tr><td><a href='#/projects/" + App.h(p.name) + "'>" + App.h(p.name) + "</a></td>";
                html += "<td>" + p.release_count + "</td>";
                html += "<td>" + p.artifact_count + "</td>";
                html += "<td>" + App.h(App.humanSize(p.total_bytes)) + "</td></tr>";
            }
        }
        html += "</tbody></table></div>";
        document.getElementById("content").innerHTML = html;
    });
};

// --- Router ---

App.route = function () {
    var hash = window.location.hash.replace(/^#\/?/, "") || "";

    var releaseM = hash.match(/^projects\/(.+)\/releases\/([^\/]+)$/);
    if (releaseM) { App.pages.release(releaseM[1], releaseM[2]); return; }

    var projectM = hash.match(/^projects\/(.+)$/);
    if (projectM) { App.pages.project(projectM[1]); return; }

    var siteM = hash.match(/^sites\/(.+)$/);
    if (siteM) { App.pages.site(siteM[1]); return; }

    var first = hash.split("/")[0];
    if (first === "projects") { App.pages.projects(); }
    else if (first === "registries") { App.pages.registries(); }
    else if (first === "sites") { App.pages.sites(); }
    else if (first === "tokens") { App.pages.tokens(); }
    else if (first === "oidc") { App.pages.oidc(); }
    else if (first === "artifacts") { App.pages.artifacts(); }
    else if (first === "storage") { App.pages.storage(); }
    else { App.pages.dashboard(); }
};

// --- Demo data ---

App.demoServices = {
    dl: "https://dl.builds.example.com",
    apt: "https://apt.builds.example.com",
    brew: "https://brew.builds.example.com",
    npm: "https://npm.builds.example.com",
    oci: "https://oci.builds.example.com",
    sites: "https://sites.builds.example.com",
    static: "https://static.builds.example.com"
};

App.demoData = {
    "/sidebar": { build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" }, build_age: "", cpu_percent: "0.0%", disk_used: "0 B", disk_total: "0 B" },
    "/dashboard": {
        stats: { project_count: 2, release_count: 5, artifact_count: 12, total_storage_bytes: 52428800, token_count: 3, site_count: 3 },
        recent: [
            { project_name: "myapp", version: "3", git_branch: "main", published: true, created_at: new Date(Date.now() - 3600000).toISOString() },
            { project_name: "cli-tool", version: "1.2.0", git_branch: "release", published: true, created_at: new Date(Date.now() - 86400000).toISOString() }
        ],
        config: { base_url: "https://builds.example.com", listen_addr: ":8080", admin_listen_addr: ":9090", data_dir: "./data", oidc_issuers: ["https://token.actions.githubusercontent.com"], oidc_orgs: ["myorg"], oidc_events: ["push"] },
        services: App.demoServices,
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
        services: App.demoServices
    },
    "/registries": { base_url: "https://builds.example.com", services: App.demoServices, projects: [{ name: "myapp", is_private: false }, { name: "cli-tool", is_private: true }] },
    "/sites": { sites: [{ project_name: "myapp", branch: "main", file_count: 12, size: 45000, git_commit: "abc123def456", updated_at: new Date(Date.now() - 3600000).toISOString() }, { project_name: "myapp", branch: "staging", file_count: 15, size: 52000, git_commit: "def456abc789", updated_at: new Date(Date.now() - 7200000).toISOString() }, { project_name: "cli-tool", branch: "main", file_count: 8, size: 23000, git_commit: "fff000111222", updated_at: new Date(Date.now() - 86400000).toISOString() }], base_url: "https://builds.example.com", services: App.demoServices },
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
    }
};

// --- Init ---

document.addEventListener("DOMContentLoaded", function () {
    if (window.location.pathname !== "/") App.demo = true;
    App.fetch("/sidebar").then(function (data) {
        App.sidebarCache = data;
        App.route();
    });
});

window.addEventListener("hashchange", function () {
    App.route();
});
