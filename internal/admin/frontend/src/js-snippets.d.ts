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
    }
    export interface TimelineMarker { time: number; label?: string; kind?: string }
    export interface TimelineHit { type: string; interval?: TimelineInterval }
    export class TimelineViewElement extends HTMLElement {
        setData(data: { lanes?: TimelineLane[]; intervals?: TimelineInterval[]; markers?: TimelineMarker[] }): void;
        setViewport(start: number, end: number): void;
        staleAfterMs: number;
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
    export class DataTableElement<Row extends object = object> extends HTMLElement {
        columns: TableColumn<Row>[];
        rows: Row[];
        rowId: ((row: Row) => string) | null;
        detailFor: ((row: Row) => Node | Promise<Node> | null) | null;
        styleText: string;
    }
}
