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
    project: Project;
    release: Release;
    artifacts: ArtifactDetail[];
    total_downloads: number;
    total_size: number;
    base_url: string;
}

export interface RegistriesData {
    base_url: string;
    projects: ProjectSummary[];
}

export interface TokenInfo {
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
