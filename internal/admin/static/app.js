"use strict";
(() => {
  // src/app.ts
  var demo = false;
  var sidebarCache = null;
  function h(s) {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }
  function humanSize(b) {
    if (b < 1024) return b + " B";
    const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
    let i = -1;
    let v = b;
    do {
      v /= 1024;
      i++;
    } while (v >= 1024 && i < units.length - 1);
    return v.toFixed(1) + " " + units[i];
  }
  function timeAgo(s) {
    if (!s) return "-";
    const d = Date.now() - new Date(s).getTime();
    if (d < 6e4) return "just now";
    const m = Math.floor(d / 6e4);
    if (m < 60) return m === 1 ? "1 minute ago" : m + " minutes ago";
    const hr = Math.floor(m / 60);
    if (hr < 24) return hr === 1 ? "1 hour ago" : hr + " hours ago";
    const days = Math.floor(hr / 24);
    return days === 1 ? "1 day ago" : days + " days ago";
  }
  function formatTime(s) {
    if (!s) return "-";
    const d = new Date(s);
    if (isNaN(d.getTime())) return "-";
    const pad = (n) => n < 10 ? "0" + n : "" + n;
    return d.getUTCFullYear() + "-" + pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate()) + " " + pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()) + " UTC";
  }
  function apiFetch(path) {
    if (demo) return Promise.resolve(demoData[path]);
    return fetch("/api" + path).then((r) => {
      if (!r.ok) throw new Error(String(r.status));
      return r.json();
    }).catch(() => {
      demo = true;
      return demoData[path];
    });
  }
  function setTitle(t) {
    document.title = t + " - Buildhost Admin";
  }
  var NAV_ITEMS = [
    { id: "dashboard", href: "#/", label: "Dashboard", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M10.707 2.293a1 1 0 00-1.414 0l-7 7a1 1 0 001.414 1.414L4 10.414V17a1 1 0 001 1h2a1 1 0 001-1v-2a1 1 0 011-1h2a1 1 0 011 1v2a1 1 0 001 1h2a1 1 0 001-1v-6.586l.293.293a1 1 0 001.414-1.414l-7-7z"/></svg>' },
    { id: "projects", href: "#/projects", label: "Projects", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>' },
    { id: "registries", href: "#/registries", label: "Registries", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4 4a2 2 0 012-2h8a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V4zm2 0h8v3H6V4zm0 5h8v2H6V9zm0 4h5v2H6v-2z" clip-rule="evenodd"/></svg>' },
    { id: "tokens", href: "#/tokens", label: "Tokens", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M18 8a6 6 0 01-7.743 5.743L10 14l-1 1-1 1H6v2H2v-4l4.257-4.257A6 6 0 1118 8zm-6-4a1 1 0 100 2 2 2 0 012 2 1 1 0 102 0 4 4 0 00-4-4z" clip-rule="evenodd"/></svg>' },
    { id: "sites", href: "#/sites", label: "Sites", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M4.083 9h1.946c.089-1.546.383-2.97.837-4.118A6.004 6.004 0 004.083 9zM10 2a8 8 0 100 16 8 8 0 000-16zm0 2c-.076 0-.232.032-.465.262-.238.234-.497.623-.737 1.182-.389.907-.673 2.142-.766 3.556h3.936c-.093-1.414-.377-2.649-.766-3.556-.24-.56-.5-.948-.737-1.182C10.232 4.032 10.076 4 10 4zm3.971 5c-.089-1.546-.383-2.97-.837-4.118A6.004 6.004 0 0115.917 9h-1.946zm-2.003 2H8.032c.093 1.414.377 2.649.766 3.556.24.56.5.948.737 1.182.233.23.389.262.465.262.076 0 .232-.032.465-.262.238-.234.497-.623.737-1.182.389-.907.673-2.142.766-3.556zm1.166 4.118c.454-1.147.748-2.572.837-4.118h1.946a6.004 6.004 0 01-2.783 4.118zm-6.268 0C6.412 13.97 6.118 12.546 6.029 11H4.083a6.004 6.004 0 002.783 4.118z" clip-rule="evenodd"/></svg>' },
    { id: "oidc", href: "#/oidc", label: "OIDC Policies", icon: '<svg viewBox="0 0 20 20" fill="currentColor" width="18" height="18"><path fill-rule="evenodd" d="M2.166 4.999A11.954 11.954 0 0010 1.944 11.954 11.954 0 0017.834 5c.11.65.166 1.32.166 2.001 0 5.225-3.34 9.67-8 11.317C5.34 16.67 2 12.225 2 7c0-.682.057-1.35.166-2.001zm11.541 3.708a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/></svg>' }
  ];
  function renderSidebar(nav) {
    const build = sidebarCache?.build;
    let links = "";
    for (const n of NAV_ITEMS) {
      links += '<li><a href="' + n.href + '"' + (n.id === nav ? ' class="active"' : "") + ">" + n.icon + " " + h(n.label) + "</a></li>";
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
    document.getElementById("sidebar").innerHTML = '<div class="sidebar-header"><div class="logo">B</div><div><div class="sidebar-title">Buildhost</div><div class="sidebar-subtitle">Admin Dashboard</div></div></div><ul class="nav-list">' + links + '</ul><div class="sidebar-footer">' + footer + "</div>";
  }
  function badge(type, text) {
    return '<span class="badge badge-' + type + '">' + h(text) + "</span>";
  }
  function urlTpl(tpl, base, suffix) {
    return '<span class="url-tpl" data-tpl="' + h(tpl) + '"><code>' + h(base) + '</code><select class="tpl-select" data-var="os"><option value="linux">linux</option><option value="darwin">darwin</option><option value="windows">windows</option><option value="freebsd">freebsd</option></select><code>/</code><select class="tpl-select" data-var="arch"><option value="amd64">amd64</option><option value="arm64">arm64</option><option value="386">386</option><option value="arm">arm</option></select>' + (suffix ? "<code>" + h(suffix) + "</code>" : "") + "</span><copy-btn></copy-btn>";
  }
  function codeBlock(label, code) {
    return '<div class="code-block"><div class="code-label">' + h(label) + '<copy-btn class="code-copy-btn" data-src="pre"></copy-btn></div><pre>' + h(code) + "</pre></div>";
  }
  function pageDashboard() {
    setTitle("Dashboard");
    renderSidebar("dashboard");
    apiFetch("/dashboard").then((d) => {
      const s = d.stats || {};
      const b = d.build || {};
      const cfg = d.config || {};
      let html = '<h1>Dashboard</h1><div class="stat-grid">';
      const cards = [
        [s.project_count, "Projects", "#/projects"],
        [s.release_count, "Releases", "#/projects"],
        [s.artifact_count, "Artifacts", "#/artifacts"],
        [humanSize(s.total_storage_bytes || 0), "Storage Used", "#/storage"],
        [s.token_count, "API Tokens", "#/tokens"],
        [s.site_count || 0, "Sites", "#/sites"]
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
      const issuers = (cfg.oidc_issuers || []).map((v) => "<code>" + h(v) + "</code>").join(", ");
      html += "<tr><td class='info-label'>Trusted OIDC Issuers</td><td>" + (issuers || '<span class="empty">None</span>') + "</td></tr>";
      const orgs = (cfg.oidc_orgs || []).map((v) => "<code>" + h(v) + "</code>").join(", ");
      html += "<tr><td class='info-label'>Allowed OIDC Orgs</td><td>" + (orgs || '<span class="empty">None</span>') + "</td></tr>";
      const events = (cfg.oidc_events || []).map((v) => "<code>" + h(v) + "</code>").join(", ");
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
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageProjects() {
    setTitle("Projects");
    renderSidebar("projects");
    apiFetch("/projects").then((projects) => {
      let html = '<h1>Projects</h1><div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Description</th><th>Versioning</th><th>Visibility</th><th>Releases</th><th>Artifacts</th><th>Created</th></tr></thead><tbody>';
      if (projects.length === 0) {
        html += '<tr><td colspan="7" class="empty">No projects yet</td></tr>';
      } else {
        for (const p of projects) {
          html += "<tr><td><a href='#/projects/" + h(p.name) + "'>" + h(p.name) + "</a></td>";
          html += '<td class="truncate">' + h(p.description) + "</td>";
          html += "<td>" + badge("neutral", p.versioning) + "</td>";
          html += "<td>" + (p.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
          html += "<td>" + p.release_count + "</td><td>" + p.artifact_count + "</td>";
          html += '<td title="' + h(formatTime(p.created_at)) + '">' + h(timeAgo(p.created_at)) + "</td></tr>";
        }
      }
      html += "</tbody></table></div>";
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageProject(name) {
    setTitle(name);
    renderSidebar("projects");
    apiFetch("/projects/" + encodeURIComponent(name)).then((d) => {
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
      html += '<div class="card"><h2>Releases</h2><table class="data-table"><thead><tr><th>Version</th><th>Branch</th><th>Commit</th><th>Status</th><th>Artifacts</th><th>Published</th><th>Created</th></tr></thead><tbody>';
      const rels = d.releases || [];
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
          html += '<td><a href="' + h(bu) + "/sites/" + h(p.name) + "/branch/" + h(si.branch) + '/" target="_blank">Open</a></td></tr>';
        }
        html += "</tbody></table></div>";
      }
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageRelease(name, version) {
    setTitle(name + " " + version);
    renderSidebar("projects");
    apiFetch("/projects/" + encodeURIComponent(name) + "/releases/" + encodeURIComponent(version)).then((d) => {
      const p = d.project, r = d.release, bu = d.base_url;
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
          html += '<a href="' + h(bu) + "/dl/" + h(p.name) + "/" + h(r.version) + "/" + h(a.os) + "/" + h(a.arch) + '" title="Direct download">raw</a>';
          if (a.debug_storage_key) html += ' <a href="' + h(bu) + "/dl/" + h(p.name) + "/" + h(r.version) + "/" + h(a.os) + "/" + h(a.arch) + '?debug=1" title="Debug symbols">debug</a>';
          const pkgs = a.packages || [];
          for (const pkg of pkgs) {
            html += ' <a href="' + h(bu) + "/dl/" + h(p.name) + "/" + h(r.version) + "/" + h(a.os) + "/" + h(a.arch) + "?format=" + h(pkg.format) + '" title="' + h(pkg.filename) + " (" + h(humanSize(pkg.size)) + ')">' + h(pkg.format) + "</a>";
          }
          html += "</td></tr>";
        }
      }
      html += "</tbody></table></div>";
      html += '<div class="card"><h2>Download Endpoints</h2><table class="info-table">';
      html += "<tr><td class='info-label'>Direct (latest)</td><td class='endpoint-cell'>" + urlTpl(bu + "/dl/" + p.name + "/latest/{os}/{arch}", bu + "/dl/" + p.name + "/latest/") + "</td></tr>";
      html += "<tr><td class='info-label'>Direct (version)</td><td class='endpoint-cell'>" + urlTpl(bu + "/dl/" + p.name + "/" + r.version + "/{os}/{arch}", bu + "/dl/" + p.name + "/" + r.version + "/") + "</td></tr>";
      if (r.git_branch) html += "<tr><td class='info-label'>Direct (branch)</td><td class='endpoint-cell'>" + urlTpl(bu + "/dl/" + p.name + "/branch/" + r.git_branch + "/{os}/{arch}", bu + "/dl/" + p.name + "/branch/" + r.git_branch + "/") + "</td></tr>";
      html += "<tr><td class='info-label'>APT</td><td class='endpoint-cell'><a href='" + h(bu) + "/apt/" + h(p.name) + "/dists/stable/Release' data-copy='" + h(bu) + "/apt/" + h(p.name) + "'>" + h(bu) + "/apt/" + h(p.name) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Homebrew</td><td class='endpoint-cell'><a href='" + h(bu) + "/brew/" + h(p.name) + ".rb'>" + h(bu) + "/brew/" + h(p.name) + ".rb</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>npm</td><td class='endpoint-cell'><a href='" + h(bu) + "/npm/@buildhost/" + h(p.name) + "'>" + h(bu) + "/npm/@buildhost/" + h(p.name) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>OCI</td><td class='endpoint-cell'><a href='" + h(bu) + "/v2/" + h(p.name) + "/manifests/" + h(r.version) + "'>" + h(bu) + "/v2/" + h(p.name) + "/manifests/" + h(r.version) + "</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "</table></div>";
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageRegistries() {
    setTitle("Registries");
    renderSidebar("registries");
    apiFetch("/registries").then((d) => {
      const bu = d.base_url;
      let html = "<h1>Registry Endpoints</h1>";
      html += '<div class="card"><h2>Direct Downloads</h2><p class="section-desc">Download artifacts directly by platform. Supports version pinning, latest, and branch-based resolution.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>Latest</td><td class='endpoint-cell'>" + urlTpl(bu + "/dl/{project}/latest/{os}/{arch}", bu + "/dl/{project}/latest/") + "</td></tr>";
      html += "<tr><td class='info-label'>Version</td><td class='endpoint-cell'><code>" + h(bu) + "/dl/{project}/{version}/{os}/{arch}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Branch</td><td class='endpoint-cell'><code>" + h(bu) + "/dl/{project}/branch/{branch}/{os}/{arch}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("curl", 'curl -fsSL -H "Authorization: Bearer $TOKEN" \\\n  ' + bu + "/dl/{project}/latest/linux/amd64 -o {project}");
      html += codeBlock("Query parameters", "?format=raw       # Default binary (stripped if available)\n?format=deb       # Debian package\n?format=brew      # Homebrew bottle\n?format=npm       # npm tarball\n?debug=1          # Debug symbols");
      html += "</div>";
      html += '<div class="card"><h2>APT Repository</h2><p class="section-desc">Debian/Ubuntu package repository. Packages are generated at publish time.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>Release</td><td class='endpoint-cell'><code>" + h(bu) + "/apt/{project}/dists/stable/Release</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>InRelease</td><td class='endpoint-cell'><code>" + h(bu) + "/apt/{project}/dists/stable/InRelease</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Packages</td><td class='endpoint-cell'><code>" + h(bu) + "/apt/{project}/dists/stable/main/binary-{arch}/Packages</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Pool</td><td class='endpoint-cell'><code>" + h(bu) + "/apt/{project}/pool/{filename}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("Setup (public project)", 'echo "deb [trusted=yes] ' + bu + '/apt/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\nsudo apt update && sudo apt install {project}');
      html += codeBlock("Setup (private project)", 'echo "deb [trusted=yes] ' + bu + '/apt/{project} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/{project}.list\ncat <<EOF | sudo tee /etc/apt/auth.conf.d/{project}.conf\nmachine ' + bu + "/apt/{project}\n  login token\n  password $TOKEN\nEOF\nsudo apt update && sudo apt install {project}");
      html += "</div>";
      html += '<div class="card"><h2>Homebrew Tap</h2><p class="section-desc">Homebrew formula served as a single .rb file. Auto-detects macOS and Linux bottles.</p>';
      html += '<table class="info-table"><tr><td class="info-label">Formula</td><td class="endpoint-cell"><code>' + h(bu) + "/brew/{project}.rb</code><copy-btn data-src='code'></copy-btn></td></tr></table>";
      html += codeBlock("Install (public project)", "brew install " + bu + "/brew/{project}.rb");
      html += codeBlock("Install (private project)", "HOMEBREW_BUILDHOST_TOKEN=$TOKEN brew install " + bu + "/brew/{project}.rb");
      html += "</div>";
      html += '<div class="card"><h2>npm Registry</h2><p class="section-desc">npm-compatible registry. Packages are scoped under <code>@buildhost</code>.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>Package metadata</td><td class='endpoint-cell'><code>" + h(bu) + "/npm/@buildhost/{project}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Tarball</td><td class='endpoint-cell'><code>" + h(bu) + "/npm/@buildhost/{project}/-/{project}-{version}.tgz</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("Setup", "npm config set @buildhost:registry " + bu + "/npm/\nnpm config set //" + bu + "/npm/:_authToken $TOKEN   # if private\nnpm install @buildhost/{project}");
      html += "</div>";
      html += '<div class="card"><h2>OCI Distribution (Docker)</h2><p class="section-desc">OCI-compatible registry for pulling artifacts as container images.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>API check</td><td class='endpoint-cell'><a href='" + h(bu) + "/v2/' data-copy='" + h(bu) + "/v2/'>" + h(bu) + "/v2/</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Manifest</td><td class='endpoint-cell'><code>" + h(bu) + "/v2/{project}/manifests/{reference}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Blob</td><td class='endpoint-cell'><code>" + h(bu) + "/v2/{project}/blobs/{digest}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("Docker pull", "docker pull " + bu + "/{project}:{version}");
      html += codeBlock("Private project", "echo $TOKEN | docker login " + bu + " -u token --password-stdin\ndocker pull " + bu + "/{project}:{version}");
      html += "</div>";
      html += '<div class="card"><h2>Static Sites</h2><p class="section-desc">Host small, self-contained static sites with independent per-branch deployments.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>Deploy</td><td class='endpoint-cell'><code>PUT " + h(bu) + "/sites/{project}/branch/{branch}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Serve</td><td class='endpoint-cell'><code>" + h(bu) + "/sites/{project}/branch/{branch}/{path}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Delete</td><td class='endpoint-cell'><code>DELETE " + h(bu) + "/sites/{project}/branch/{branch}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>List</td><td class='endpoint-cell'><code>GET " + h(bu) + "/api/v1/projects/{project}/sites</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("Deploy", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project {project} \\\n  --branch {branch} \\\n  --dir ./dist");
      html += "</div>";
      html += '<div class="card"><h2>REST API</h2><p class="section-desc">JSON API for managing projects, releases, and artifacts programmatically.</p>';
      html += '<table class="info-table">';
      html += "<tr><td class='info-label'>List projects</td><td class='endpoint-cell'><a href='" + h(bu) + "/api/v1/projects'>GET " + h(bu) + "/api/v1/projects</a><copy-btn data-src='a'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Get project</td><td class='endpoint-cell'><code>GET " + h(bu) + "/api/v1/projects/{project}</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>List releases</td><td class='endpoint-cell'><code>GET " + h(bu) + "/api/v1/projects/{project}/releases</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "<tr><td class='info-label'>Publish</td><td class='endpoint-cell'><code>POST " + h(bu) + "/api/v1/projects/{project}/publish</code><copy-btn data-src='code'></copy-btn></td></tr>";
      html += "</table>";
      html += codeBlock("Authentication", '# Bearer token\ncurl -H "Authorization: Bearer $TOKEN" ' + bu + '/api/v1/projects\n\n# Basic auth\ncurl -u "token:$TOKEN" ' + bu + `/api/v1/projects

# Query parameter (for clients that can't set headers)
curl "` + bu + '/api/v1/projects?token=$TOKEN"');
      html += "</div>";
      const projects = d.projects || [];
      if (projects.length > 0) {
        html += '<div class="card"><h2>Projects</h2><p class="section-desc">Quick links to project-specific endpoints.</p>';
        html += '<table class="data-table"><thead><tr><th>Project</th><th>Visibility</th><th>Direct Download</th><th>APT</th><th>Brew</th><th>npm</th></tr></thead><tbody>';
        for (const pr of projects) {
          html += "<tr><td><a href='#/projects/" + h(pr.name) + "'>" + h(pr.name) + "</a></td>";
          html += "<td>" + (pr.is_private ? badge("warning", "Private") : badge("success", "Public")) + "</td>";
          html += "<td class='endpoint-cell'><span class='url-tpl' data-tpl='" + h(bu) + "/dl/" + h(pr.name) + "/latest/{os}/{arch}'><code class='truncate'>" + h(bu) + "/dl/" + h(pr.name) + "/latest/</code><select class='tpl-select tpl-select-sm' data-var='os'><option value='linux'>linux</option><option value='darwin'>darwin</option><option value='windows'>windows</option><option value='freebsd'>freebsd</option></select><code>/</code><select class='tpl-select tpl-select-sm' data-var='arch'><option value='amd64'>amd64</option><option value='arm64'>arm64</option><option value='386'>386</option><option value='arm'>arm</option></select></span><copy-btn></copy-btn></td>";
          html += "<td class='endpoint-cell'><a href='" + h(bu) + "/apt/" + h(pr.name) + "/dists/stable/Release' data-copy='" + h(bu) + "/apt/" + h(pr.name) + "'>" + h(bu) + "/apt/" + h(pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
          html += "<td class='endpoint-cell'><a href='" + h(bu) + "/brew/" + h(pr.name) + ".rb'>" + h(bu) + "/brew/" + h(pr.name) + ".rb</a><copy-btn data-src='a'></copy-btn></td>";
          html += "<td class='endpoint-cell'><a href='" + h(bu) + "/npm/@buildhost/" + h(pr.name) + "'>" + h(bu) + "/npm/@buildhost/" + h(pr.name) + "</a><copy-btn data-src='a'></copy-btn></td>";
          html += "</tr>";
        }
        html += "</tbody></table></div>";
      }
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageTokens() {
    setTitle("Tokens");
    renderSidebar("tokens");
    apiFetch("/tokens").then((tokens) => {
      let html = '<h1>API Tokens</h1><div class="card"><table class="data-table"><thead><tr><th>Name</th><th>Prefix</th><th>Scope</th><th>Project</th><th>Permissions</th><th>Created</th><th>Last Used</th><th>Expires</th></tr></thead><tbody>';
      if (tokens.length === 0) {
        html += '<tr><td colspan="8" class="empty">No tokens yet</td></tr>';
      } else {
        for (const t of tokens) {
          html += "<tr" + (t.is_expired ? ' class="row-muted"' : "") + ">";
          html += "<td>" + h(t.name) + "</td>";
          html += "<td><code>" + h(t.token_prefix) + "...</code></td>";
          html += "<td>" + (t.is_global ? badge("neutral", "Global") : badge("info", "Project")) + "</td>";
          html += "<td>" + (t.project_name ? "<a href='#/projects/" + h(t.project_name) + "'>" + h(t.project_name) + "</a>" : "-") + "</td>";
          html += "<td><code>" + h(t.scopes) + "</code></td>";
          html += '<td title="' + h(formatTime(t.created_at)) + '">' + h(timeAgo(t.created_at)) + "</td>";
          html += "<td>" + (t.last_used_at ? h(formatTime(t.last_used_at)) : '<span class="muted">Never</span>') + "</td>";
          let exp = "";
          if (t.expires_at) {
            if (t.is_expired) exp += badge("danger", "Expired") + " ";
            exp += h(formatTime(t.expires_at));
          } else {
            exp = '<span class="muted">Never</span>';
          }
          html += "<td>" + exp + "</td></tr>";
        }
      }
      html += "</tbody></table></div>";
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageSites() {
    setTitle("Sites");
    renderSidebar("sites");
    apiFetch("/sites").then((d) => {
      const bu = d.base_url || "";
      const sites = d.sites || [];
      const byProject = {};
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
      html += codeBlock("curl", 'tar czf - -C ./dist . | curl -X PUT \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H "Content-Type: application/gzip" \\\n  --data-binary @- \\\n  ' + bu + "/sites/{project}/branch/{branch}");
      html += "</div>";
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageSite(name) {
    setTitle(name + " - Sites");
    renderSidebar("sites");
    apiFetch("/projects/" + encodeURIComponent(name)).then((d) => {
      const p = d.project;
      const bu = d.base_url || "";
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
          html += '<td><a href="' + h(bu) + "/sites/" + h(p.name) + "/branch/" + h(s.branch) + '/" target="_blank">Open</a></td></tr>';
        }
      }
      html += "</tbody></table></div>";
      html += '<div class="card"><h2>Deploy to ' + h(p.name) + "</h2>";
      html += codeBlock("CLI", "buildhost publish-site \\\n  --server " + bu + " \\\n  --token $TOKEN \\\n  --project " + p.name + " \\\n  --branch {branch} \\\n  --dir ./dist");
      html += codeBlock("Delete a branch", 'curl -X DELETE \\\n  -H "Authorization: Bearer $TOKEN" \\\n  ' + bu + "/sites/" + p.name + "/branch/{branch}");
      html += "</div>";
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageOIDC() {
    setTitle("OIDC Policies");
    renderSidebar("oidc");
    apiFetch("/oidc").then((policies) => {
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
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageArtifacts() {
    setTitle("Artifacts");
    renderSidebar("dashboard");
    apiFetch("/artifacts").then((artifacts) => {
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
      document.getElementById("content").innerHTML = html;
    });
  }
  function pageStorage() {
    setTitle("Storage");
    renderSidebar("dashboard");
    apiFetch("/storage").then((d) => {
      const projects = d.projects || [];
      let html = '<h1>Storage Usage</h1><div class="stat-grid">';
      html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.total_bytes || 0)) + '</div><div class="stat-label">Artifact Storage</div></div>';
      html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.logical_bytes || 0)) + '</div><div class="stat-label">Logical Size</div></div>';
      html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.physical_bytes || 0)) + '</div><div class="stat-label">Physical Size (dedup)</div></div>';
      html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_bytes || 0)) + '</div><div class="stat-label">Blobs on Disk</div></div>';
      if (d.disk_total) {
        html += '<div class="stat-card"><div class="stat-value">' + h(humanSize(d.disk_used || 0)) + " / " + h(humanSize(d.disk_total || 0)) + '</div><div class="stat-label">Filesystem Usage</div></div>';
      }
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
      document.getElementById("content").innerHTML = html;
    });
  }
  function route() {
    const hash = window.location.hash.replace(/^#\/?/, "") || "";
    const parts = hash.split("/").map(decodeURIComponent);
    if (parts[0] === "projects" && parts.length === 4 && parts[2] === "releases") {
      pageRelease(parts[1], parts[3]);
    } else if (parts[0] === "projects" && parts.length === 2) {
      pageProject(parts[1]);
    } else if (parts[0] === "projects") {
      pageProjects();
    } else if (parts[0] === "registries") {
      pageRegistries();
    } else if (parts[0] === "sites" && parts.length === 2) {
      pageSite(parts[1]);
    } else if (parts[0] === "sites") {
      pageSites();
    } else if (parts[0] === "tokens") {
      pageTokens();
    } else if (parts[0] === "oidc") {
      pageOIDC();
    } else if (parts[0] === "artifacts") {
      pageArtifacts();
    } else if (parts[0] === "storage") {
      pageStorage();
    } else {
      pageDashboard();
    }
  }
  var demoData = {
    "/sidebar": { build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" }, build_age: "", cpu_percent: "0.0%", disk_used: "0 B", disk_total: "0 B" },
    "/dashboard": {
      stats: { project_count: 2, release_count: 5, artifact_count: 12, total_storage_bytes: 52428800, token_count: 3, site_count: 3 },
      recent: [
        { project_name: "myapp", version: "3", git_branch: "main", published: true, created_at: new Date(Date.now() - 36e5).toISOString() },
        { project_name: "cli-tool", version: "1.2.0", git_branch: "release", published: true, created_at: new Date(Date.now() - 864e5).toISOString() }
      ],
      config: { base_url: "https://builds.example.com", listen_addr: ":8080", admin_listen_addr: ":9090", data_dir: "./data", oidc_issuers: ["https://token.actions.githubusercontent.com"], oidc_orgs: ["myorg"], oidc_events: ["push"] },
      build: { version: "v0.0.0-demo", commit: "demo", commit_url: "", short_commit: "demo", date: "" },
      uptime: "0m 0s",
      cpu_percent: "0.0%",
      cpu_total: "0m 0s"
    },
    "/projects": [
      { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, release_count: 3, artifact_count: 8, created_at: new Date(Date.now() - 864e5 * 30).toISOString() },
      { id: 2, name: "cli-tool", description: "CLI utility", versioning: "semver", is_private: true, release_count: 2, artifact_count: 4, created_at: new Date(Date.now() - 864e5 * 10).toISOString() }
    ],
    "/projects/myapp": {
      project: { id: 1, name: "myapp", description: "Main application", versioning: "auto", is_private: false, created_at: new Date(Date.now() - 864e5 * 30).toISOString(), updated_at: new Date(Date.now() - 36e5).toISOString() },
      releases: [{ version: "3", git_branch: "main", git_commit: "abc123", published: true, artifact_count: 4, published_at: new Date(Date.now() - 36e5).toISOString(), created_at: new Date(Date.now() - 36e5).toISOString() }],
      sites: [{ branch: "main", file_count: 12, size: 45e3, git_commit: "abc123def456", updated_at: new Date(Date.now() - 36e5).toISOString() }, { branch: "staging", file_count: 15, size: 52e3, git_commit: "def456abc789", updated_at: new Date(Date.now() - 72e5).toISOString() }],
      base_url: "https://builds.example.com"
    },
    "/registries": { base_url: "https://builds.example.com", projects: [{ name: "myapp", is_private: false }, { name: "cli-tool", is_private: true }] },
    "/sites": { sites: [{ project_name: "myapp", branch: "main", file_count: 12, size: 45e3, git_commit: "abc123def456", updated_at: new Date(Date.now() - 36e5).toISOString() }, { project_name: "myapp", branch: "staging", file_count: 15, size: 52e3, git_commit: "def456abc789", updated_at: new Date(Date.now() - 72e5).toISOString() }, { project_name: "cli-tool", branch: "main", file_count: 8, size: 23e3, git_commit: "fff000111222", updated_at: new Date(Date.now() - 864e5).toISOString() }], base_url: "https://builds.example.com" },
    "/tokens": [{ name: "deploy", token_prefix: "bh_abc", is_global: false, project_name: "myapp", scopes: "read,write", is_expired: false, created_at: new Date(Date.now() - 864e5 * 7).toISOString(), last_used_at: new Date(Date.now() - 36e5).toISOString() }],
    "/oidc": [{ issuer: "https://token.actions.githubusercontent.com", subject_pattern: "repo:myorg/myapp:*", audience: "", project_name: "myapp", scopes: "read,write", created_at: new Date(Date.now() - 864e5 * 14).toISOString() }],
    "/artifacts": [
      { id: 1, os: "linux", arch: "amd64", kind: "binary", size: 15728640, filename: "myapp", created_at: new Date(Date.now() - 36e5).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 42 },
      { id: 2, os: "darwin", arch: "arm64", kind: "binary", size: 14680064, filename: "myapp", created_at: new Date(Date.now() - 36e5).toISOString(), version: "3", git_branch: "main", project_name: "myapp", download_count: 18 },
      { id: 3, os: "linux", arch: "amd64", kind: "binary", size: 10485760, filename: "cli-tool", created_at: new Date(Date.now() - 864e5).toISOString(), version: "1.2.0", git_branch: "release", project_name: "cli-tool", download_count: 7 }
    ],
    "/storage": {
      projects: [
        { id: 1, name: "myapp", total_bytes: 45e6, artifact_count: 8, release_count: 3 },
        { id: 2, name: "cli-tool", total_bytes: 7428800, artifact_count: 4, release_count: 2 }
      ],
      total_bytes: 52428800,
      logical_bytes: 58e6,
      physical_bytes: 48e6,
      disk_bytes: 5e7,
      disk_used: 12e7,
      disk_total: 5e8
    }
  };
  document.addEventListener("DOMContentLoaded", () => {
    if (window.location.pathname !== "/") demo = true;
    apiFetch("/sidebar").then((data) => {
      sidebarCache = data;
      route();
    });
  });
  window.addEventListener("hashchange", () => {
    route();
  });
})();
