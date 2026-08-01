// Tiny HTML builder. Nested calls become an escaped HTML string:
//
//   Html.div(Html.h2("Title", Html.span("note").cls("muted"))).cls("card")
//
// Text children are HTML-escaped by default (so a forgotten escape can't become
// an injection); element children and Html.raw(...) render verbatim; arrays
// flatten; null/undefined/false/true are skipped (so `cond && node` works).
// Elements stringify via toString(), so `"" + Html.div(...)` works anywhere a
// string is expected.

export type Node = El | Raw | string | number | null | undefined | boolean | Node[];

export function escape(s: unknown): string {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

// Raw marks an already-built, trusted HTML string so the builder emits it
// verbatim instead of escaping it (e.g. the output of codeBlock/urlTpl).
export class Raw {
    html: string;
    constructor(html: unknown) {
        this.html = html == null ? "" : String(html);
    }
    toString(): string {
        return this.html;
    }
}

export function raw(html: unknown): Raw {
    return new Raw(html);
}

const VOID_TAGS: Record<string, boolean> = {
    area: true, base: true, br: true, col: true, embed: true, hr: true, img: true,
    input: true, link: true, meta: true, param: true, source: true, track: true, wbr: true,
};

export class El {
    tag: string;
    a: Record<string, string | boolean | null | undefined>;
    kids: Node[];
    constructor(tag: string, kids?: Node[]) {
        this.tag = tag;
        this.a = {};
        this.kids = kids || [];
    }
    attr(k: string, v: string | boolean | null | undefined): this {
        this.a[k] = v;
        return this;
    }
    cls(v: string): this {
        this.a["class"] = v;
        return this;
    }
    style(v: string): this {
        this.a.style = v;
        return this;
    }
    add(...kids: Node[]): this {
        for (const kid of kids) this.kids.push(kid);
        return this;
    }
    toString(): string {
        let attrs = "";
        for (const k of Object.keys(this.a)) {
            const v = this.a[k];
            if (v == null || v === false) continue;
            if (v === true) {
                attrs += " " + k;
                continue;
            }
            attrs += " " + k + '="' + escape(v) + '"';
        }
        if (VOID_TAGS[this.tag]) return "<" + this.tag + attrs + ">";
        return "<" + this.tag + attrs + ">" + render(this.kids) + "</" + this.tag + ">";
    }
}

// render stringifies any child node: element/raw verbatim, array flattened,
// anything else escaped as text.
export function render(node: Node): string {
    if (node == null || node === false || node === true) return "";
    if (node instanceof El || node instanceof Raw) return node.toString();
    if (Array.isArray(node)) {
        let out = "";
        for (const child of node) out += render(child);
        return out;
    }
    return escape(node);
}

// el(tag, ...children) builds an arbitrary/custom tag (e.g. "copy-btn"); the
// common tags also get a shorthand below.
export function el(tag: string, ...kids: Node[]): El {
    return new El(tag, kids);
}

const TAGS = ["div", "span", "p", "a", "h1", "h2", "h3", "code", "pre", "strong", "sub", "ul", "li",
    "table", "thead", "tbody", "tr", "th", "td", "form", "label", "button"] as const;

type TagFn = (...kids: Node[]) => El;

export const Html: { escape: typeof escape; raw: typeof raw; render: typeof render; el: typeof el } & Record<(typeof TAGS)[number], TagFn> = {
    escape, raw, render, el,
} as never;

for (const tag of TAGS) {
    (Html as unknown as Record<string, TagFn>)[tag] = (...kids: Node[]): El => new El(tag, kids);
}
