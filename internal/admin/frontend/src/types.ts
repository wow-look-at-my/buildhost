export interface BuildInfo {
    version: string;
    commit: string;
    commit_url: string;
    short_commit: string;
    date: string;
}

export interface SidebarData {
    build: BuildInfo;
    build_age: string;
    cpu_percent: string;
    disk_used: string;
    disk_total: string;
}

export interface DashboardStats {
    project_count: number;
    release_count: number;
    artifact_count: number;
    total_storage_bytes: number;
    token_count: number;
    site_count: number;
}

export interface DashboardConfig {
    base_url: string;
    listen_addr: string;
    admin_listen_addr: string;
    data_dir: string;
    oidc_issuers: string[];
    oidc_orgs: string[];
    oidc_events: string[];
}

export interface RecentRelease {
    project_name: string;
    version: string;
    git_branch: string;
    published: boolean;
    created_at: string;
}

export interface DashboardData {
    services: ServiceURLs;
    stats: DashboardStats;
    recent: RecentRelease[];
    config: DashboardConfig;
    build: BuildInfo;
    uptime: string;
    cpu_percent: string;
    cpu_total: string;
}

export interface ProjectSummary {
    id: number;
    name: string;
    description: string;
    versioning: string;
    is_private: boolean;
    release_count: number;
    artifact_count: number;
    created_at: string;
}

export interface Project {
    id: number;
    name: string;
    description: string;
    homepage: string;
    license: string;
    versioning: string;
    is_private: boolean;
    created_at: string;
    updated_at: string;
}

export interface ReleaseSummary {
    version: string;
    git_branch: string;
    git_commit: string;
    published: boolean;
    artifact_count: number;
    published_at: string;
    created_at: string;
}

export interface SiteInfo {
    branch: string;
    file_count: number;
    size: number;
    git_commit: string;
    updated_at: string;
}

export interface ProjectData {
    services: ServiceURLs;
    project: Project;
    releases: ReleaseSummary[];
    sites: SiteInfo[];
    base_url: string;
}

export interface Release {
    version: string;
    published: boolean;
    git_branch: string;
    git_commit: string;
    notes: string;
    published_at: string;
    created_at: string;
}

export interface PackageInfo {
    format: string;
    filename: string;
    size: number;
}

export interface ArtifactDetail {
    os: string;
    arch: string;
    kind: string;
    filename: string;
    size: number;
    download_count: number;
    debug_storage_key: string;
    packages: PackageInfo[];
}

export interface ReleaseData {
    services: ServiceURLs;
    project: Project;
    release: Release;
    artifacts: ArtifactDetail[];
    total_downloads: number;
    total_size: number;
    base_url: string;
}

export interface RegistriesData {
    services: ServiceURLs;
    base_url: string;
    projects: ProjectSummary[];
}

export interface TokenInfo {
    id: number;
    name: string;
    token_prefix: string;
    is_global: boolean;
    project_name: string;
    scopes: string;
    is_expired: boolean;
    created_at: string;
    last_used_at: string | null;
    expires_at: string | null;
}

export interface SiteDetail {
    project_name: string;
    branch: string;
    file_count: number;
    size: number;
    git_commit: string;
    updated_at: string;
}

export interface SitesData {
    services: ServiceURLs;
    sites: SiteDetail[];
    base_url: string;
}

export interface OIDCPolicy {
    issuer: string;
    subject_pattern: string;
    audience: string;
    project_name: string;
    scopes: string;
    created_at: string;
}

export interface AllArtifact {
    id: number;
    os: string;
    arch: string;
    kind: string;
    size: number;
    filename: string;
    created_at: string;
    version: string;
    git_branch: string;
    project_name: string;
    download_count: number;
}

export interface StorageProject {
    id: number;
    name: string;
    total_bytes: number;
    artifact_count: number;
    release_count: number;
}

export interface StorageData {
    projects: StorageProject[];
    total_bytes: number;
    stripped_bytes: number;
    debug_bytes: number;
    packaged_bytes: number;
    reclaimable_bytes: number;
    logical_bytes: number;
    physical_bytes: number;
    disk_bytes: number;
    disk_used: number;
    disk_total: number;
}

export interface NavItem {
    id: string;
    href: string;
    label: string;
    icon: string;
}

export interface ProjectGroupInfo {
    branches: number;
    total_size: number;
    total_files: number;
    last_updated: string;
}

// --- Retention (admin GET/PUT /api/retention, POST /api/retention/run) ---

export interface RetentionRelease {
    project_id: number;
    project_name: string;
    branch: string;
    version: string;
    reason: string;
}

export interface RetentionPreview {
    release_count: number;
    reclaimable_bytes: number;
    blobs: number;
    blobs_retained: number;
    releases: RetentionRelease[];
}

export interface RetentionData {
    keep_n: number;
    recency_hours: number;
    sweeper_enabled: boolean;
    sweeper_enforce: boolean;
    preview: RetentionPreview;
}

// --- Project tree (the Projects page groups slash-namespaced names) ---

export interface TreeRow {
    kind: "folder" | "project";
    depth: number;
    name?: string;
    project?: ProjectSummary;
}

// --- Page registry ---

export interface Pages {
    dashboard(): void;
    projects(): void;
    project(name: string): void;
    release(name: string, version: string): void;
    registries(): void;
    tokens(): void;
    sites(): void;
    site(name: string): void;
    oidc(): void;
    artifacts(): void;
    storage(): void;
    retention(): void;
    goproxy(): void;
}

// --- Signed temporary download link (admin POST .../download-links) ---

export interface DownloadLink {
    url: string;
}

// serviceURLs(r) in internal/admin/admin.go: one absolute base URL per service
// subdomain, derived per request. Present on the dashboard, project, release
// and registries payloads.
export interface ServiceURLs {
    dl: string;
    apt: string;
    brew: string;
    npm: string;
    oci: string;
    sites: string;
    static: string;
}

// --- Go module proxy (GET /api/goproxy) ---

export interface GoproxyHealth {
    healthy: boolean;
    reason?: string;
    credential_configured: boolean;
    credential_kind: string;
    private_prefixes: string[] | null;
    upstream: string;
    readiness_module: string;
    probed: boolean;
    probe_version?: string;
    probe_error?: string;
    probe_error_kind?: string;
    checked_at: string;
}

export interface GoproxyModule {
    path: string;
    source: string;
    private: boolean;
    versions: number;
    bytes: number;
    last_error_kind: string;
    last_error: string;
    last_error_at?: string;
    last_success_at?: string;
    last_fetched_at?: string;
}

export interface GoproxyEvent {
    at: string;
    module: string;
    version: string;
    endpoint: string;
    source: string;
    outcome: string;
    status: number;
    detail: string;
    duration: string;
}

export interface GoproxyState {
    health: GoproxyHealth;
    cache: { modules: number; versions: number; zips: number; bytes: number; failing_modules: number };
    traffic: {
        since_start: boolean;
        cache_hits: number;
        cache_misses: number;
        fetches: number;
        bytes_sent: number;
        errors: Record<string, number> | null;
    };
    modules: GoproxyModule[] | null;
    recent: GoproxyEvent[] | null;
}

export interface GoproxyData {
    enabled: boolean;
    state?: GoproxyState;
}
