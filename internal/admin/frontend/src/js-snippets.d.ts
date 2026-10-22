// The js-snippets modules the dashboard imports at run time, typed to what it uses.

declare module "https://sites.pazer.build/js-snippets/@library/ui/timeline-view.js" {
    export interface TimelineLane { id: string; label: string; group?: string }
    export interface TimelineSegment { start: number; end?: number | null; kind: string }
    export interface TimelineInterval {
        id: string;
        laneId: string;
        start: number;
        end?: number | null;
        label?: string;
        category?: string;
        state?: string;
        segments?: TimelineSegment[];
        data?: unknown;
    }
    export interface TimelineMarker { time: number; label?: string; kind?: string }
    export interface TimelineHit { type: string; interval?: TimelineInterval }
    export class TimelineViewElement extends HTMLElement {
        setData(data: { lanes?: TimelineLane[]; intervals?: TimelineInterval[]; markers?: TimelineMarker[] }): void;
        setViewport(start: number, end: number): void;
        staleAfterMs: number;
        minTime: number | null;
        tooltipFor: ((hit: TimelineHit) => string | Node | null) | null;
    }
}

declare module "https://sites.pazer.build/js-snippets/@library/ui/data-table.js" {
    export interface TableColumn<Row> {
        key: string;
        label: string;
        value?: (row: Row) => unknown;
        render?: (row: Row) => Node | string | null;
        text?: (row: Row) => string;
        sortable?: boolean;
        align?: "start" | "end";
    }
    export interface FacetGroup<Row> {
        key: string;
        label?: string;
        of: (row: Row) => string | null | undefined;
        order?: readonly string[];
    }
    export class DataTableElement<Row extends object = object> extends HTMLElement {
        columns: TableColumn<Row>[];
        rows: Row[];
        facets: FacetGroup<Row>[];
        sort: { key: string; dir: "asc" | "desc" } | null;
        rowId: ((row: Row) => string) | null;
        detailFor: ((row: Row) => Node | Promise<Node> | null) | null;
        emptyText: string;
        styleText: string;
    }
}

declare module "https://sites.pazer.build/js-snippets/@library/ui/bar-chart.js" {
    export interface BarSeries { key: string; label?: string; color?: string; muted?: boolean }
    export interface BarRow { id: string; label: string; values: Record<string, number>; href?: string; detail?: string }
    export interface BarMarker { value: number; label: string; kind?: "default" | "warning" | "critical" }
    export class BarChartElement extends HTMLElement {
        series: BarSeries[];
        rows: BarRow[];
        markers: BarMarker[];
        max: number | null;
        format: (value: number) => string;
        showValues: boolean;
        legend: boolean;
    }
}
