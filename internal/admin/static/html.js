"use strict";

// Tiny standalone HTML builder. No dependency on App (or any other global) -- it
// just turns nested calls into an escaped HTML string:
//
//   Html.div(Html.h2("Title", Html.span("note").cls("muted"))).cls("card")
//
// Text children are HTML-escaped by default (so a forgotten escape can't become
// an injection); element children and Html.raw(...) render verbatim; arrays
// flatten; null/undefined/false/true are skipped (so `cond && node` works).
// Elements stringify via toString(), so `"" + Html.div(...)` works anywhere a
// string is expected.

var Html = {};

// escape renders text HTML-safe (the five entities the DOM needs).
Html.escape = function (s) {
    if (s == null) return "";
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
};

// raw marks an already-built, trusted HTML string so the builder emits it
// verbatim instead of escaping it (e.g. the output of other string-building
// helpers).
Html.Raw = function (html) { this.html = html == null ? "" : String(html); };
Html.Raw.prototype.toString = function () { return this.html; };
Html.raw = function (html) { return new Html.Raw(html); };

Html._VOID = { area: 1, base: 1, br: 1, col: 1, embed: 1, hr: 1, img: 1, input: 1, link: 1, meta: 1, param: 1, source: 1, track: 1, wbr: 1 };

Html.El = function (tag, kids) { this.tag = tag; this.a = {}; this.kids = kids || []; };
Html.El.prototype.attr = function (k, v) { this.a[k] = v; return this; };
Html.El.prototype.cls = function (v) { this.a["class"] = v; return this; };
Html.El.prototype.style = function (v) { this.a.style = v; return this; };
Html.El.prototype.add = function () { for (var i = 0; i < arguments.length; i++) this.kids.push(arguments[i]); return this; };
Html.El.prototype.toString = function () {
    var attrs = "";
    for (var k in this.a) {
        if (!Object.prototype.hasOwnProperty.call(this.a, k)) continue;
        var v = this.a[k];
        if (v == null || v === false) continue;
        if (v === true) { attrs += " " + k; continue; }
        attrs += " " + k + '="' + Html.escape(v) + '"';
    }
    if (Html._VOID[this.tag]) return "<" + this.tag + attrs + ">";
    return "<" + this.tag + attrs + ">" + Html.render(this.kids) + "</" + this.tag + ">";
};

// render stringifies any child node: element/raw verbatim, array flattened,
// anything else escaped as text.
Html.render = function (node) {
    if (node == null || node === false || node === true) return "";
    if (node instanceof Html.El || node instanceof Html.Raw) return node.toString();
    if (Array.isArray(node)) {
        var out = "";
        for (var i = 0; i < node.length; i++) out += Html.render(node[i]);
        return out;
    }
    return Html.escape(node);
};

// Html.el(tag, ...children) builds an arbitrary/custom tag (e.g. "copy-btn");
// the common tags also get a shorthand Html.<tag>(...children).
Html.el = function (tag) { return new Html.El(tag, Array.prototype.slice.call(arguments, 1)); };
["div", "span", "p", "a", "h1", "h2", "h3", "code", "pre", "strong", "sub", "ul", "li",
    "table", "thead", "tbody", "tr", "th", "td", "form", "label", "button"].forEach(function (tag) {
        Html[tag] = function () { return new Html.El(tag, Array.prototype.slice.call(arguments)); };
    });
