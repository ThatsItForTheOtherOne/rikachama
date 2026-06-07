package main

import (
	"strings"
	"testing"
)

func TestPostLinkBasic(t *testing.T) {
	got := string(formatBody(">>123"))
	if !strings.Contains(got, `href="/post/123"`) {
		t.Errorf("expected href=/post/123, got %q", got)
	}
}

func TestPostLinkHasQuotelinkClass(t *testing.T) {
	got := string(formatBody(">>123"))
	if !strings.Contains(got, `class="quotelink"`) {
		t.Errorf("expected class=quotelink, got %q", got)
	}
}

func TestPostLinkPreservesDisplayText(t *testing.T) {
	got := string(formatBody(">>123"))
	if !strings.Contains(got, "&gt;&gt;123") {
		t.Errorf("expected >>123 visible in output, got %q", got)
	}
}

func TestPostLinkOnlyWrapsReference(t *testing.T) {
	got := string(formatBody("look at >>123 cool"))
	if !strings.Contains(got, `123</a> cool`) {
		t.Errorf("anchor should close right after >>123, got %q", got)
	}
	if !strings.Contains(got, `look at <a`) {
		t.Errorf("text before >> should be outside the anchor, got %q", got)
	}
}

func TestPostLinkInMiddleOfLine(t *testing.T) {
	got := string(formatBody("look at >>123 cool"))
	if !strings.Contains(got, `>>123`) && !strings.Contains(got, `href="/post/123"`) {
		t.Errorf("expected both links, got %q", got)
	}
}

func TestSingleGtIsNotPostLink(t *testing.T) {
	got := string(formatBody(">1"))
	if strings.Contains(got, `href="/post/1"`) {
		t.Errorf("single > shouldn't trigger post link, got %q", got)
	}
}

func TestPostLinkRequiresDigits(t *testing.T) {
	got := string(formatBody(">>abc"))
	if strings.Contains(got, `href="/post/abc"`) {
		t.Errorf(">>abc shouldn't be a link, got %q", got)
	}
}

func TestPostLinkDoesNotBreakGreentext(t *testing.T) {
	// Greentext should still work on lines that don't start with >>
	got := string(formatBody(">just a quote\n>>123"))
	if !strings.Contains(got, `class="quote"`) {
		t.Errorf("expected greentext span, got %q", got)
	}
	if !strings.Contains(got, `class="quotelink"`) {
		t.Errorf("expected quotelink, got %q", got)
	}
}

func TestPostLinkLineNotWrappedAsGreentext(t *testing.T) {
	got := string(formatBody(">>123"))
	if strings.Contains(got, `<span class="quote">`) {
		t.Errorf(">>123 line shouldn't be greentext-wrapped, got %q", got)
	}
}

func TestPostLinkHTMLEscaping(t *testing.T) {
	// Body is escaped before linking; raw HTML must not leak through
	got := string(formatBody(`<script>alert(1)</script> >>123`))
	if strings.Contains(got, "<script>") {
		t.Errorf("script tag must be escaped, got %q", got)
	}
	if !strings.Contains(got, `href="/post/123"`) {
		t.Errorf("link should still work after escaping, got %q", got)
	}
}
