#!/bin/sh
#
# changelog.sh -- assemble changelog fragments into CHANGELOG.md at release.
#
# A maintainer command, run by hand. Never run by CI.
#
#   tools/changelog.sh preview
#       Print the sections the next release will contain, in Keep a Changelog
#       order, and change nothing.
#
#   tools/changelog.sh release <version> <date>
#       Splice those sections into CHANGELOG.md under a new "## [<version>] —
#       <date>" heading, then delete the consumed fragments.
#
# A fragment is a Markdown file in .changes/unreleased/ named
# <type>-<slug>.md, where <type> is one of the Keep a Changelog section names:
# added, changed, deprecated, removed, fixed, security. The filename carries
# the type, so there is no front matter. Sections are emitted in Keep a
# Changelog order, and within a section the fragments are read in filename
# order, so the assembled changelog is deterministic.
#
# A fragment whose filename has an unknown type prefix is a typo and an error:
# the script exits non-zero before writing anything rather than silently
# dropping an entry from the release. The script does not touch the link
# definitions at the bottom of CHANGELOG.md; those are edited by hand.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(dirname -- "$SCRIPT_DIR")
CHANGELOG="$ROOT/CHANGELOG.md"
FRAGMENTS="$ROOT/.changes/unreleased"

KNOWN_TYPES="added changed deprecated removed fixed security"

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/changelog.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT HUP INT TERM

usage() {
    echo "usage: changelog.sh preview" >&2
    echo "       changelog.sh release <version> <date>" >&2
    exit 2
}

type_title() {
    case "$1" in
        added) echo "Added" ;;
        changed) echo "Changed" ;;
        deprecated) echo "Deprecated" ;;
        removed) echo "Removed" ;;
        fixed) echo "Fixed" ;;
        security) echo "Security" ;;
        *) echo "" ;;
    esac
}

validate() {
    [ -d "$FRAGMENTS" ] || return 0
    for f in "$FRAGMENTS"/*; do
        [ -f "$f" ] || continue
        name=${f##*/}
        case "$name" in
            .*) continue ;;
        esac
        case "$name" in
            *.md) ;;
            *)
                echo "changelog: '$name' is not a fragment (expected <type>-<slug>.md)" >&2
                exit 1
                ;;
        esac
        type=${name%%-*}
        valid=0
        for t in $KNOWN_TYPES; do
            if [ "$type" = "$t" ]; then
                valid=1
                break
            fi
        done
        if [ "$valid" -ne 1 ]; then
            echo "changelog: '$name' has unknown type prefix '$type' (expected one of: $KNOWN_TYPES)" >&2
            exit 1
        fi
    done
}

types_for() {
    [ -d "$FRAGMENTS" ] || return 0
    for f in "$FRAGMENTS/$1-"*.md; do
        [ -f "$f" ] || continue
        printf '%s\n' "$f"
    done | LC_ALL=C sort
}

append_body() {
    out=$1
    f=$2
    awk '
        { lines[NR] = $0 }
        END {
            n = NR
            while (n > 0 && lines[n] == "") n--
            for (i = 1; i <= n; i++) print lines[i]
            print ""
        }
    ' "$f" >> "$out"
}

build_section() {
    out=$1
    : > "$out"
    for t in added changed deprecated removed fixed security; do
        files=$(types_for "$t")
        [ -n "$files" ] || continue
        printf '### %s\n\n' "$(type_title "$t")" >> "$out"
        for f in $files; do
            append_body "$out" "$f"
        done
    done
}

preview() {
    validate
    build_section "$TMP_DIR/section.md"
    cat "$TMP_DIR/section.md"
}

release() {
    version=$1
    date=$2
    validate
    build_section "$TMP_DIR/section.md"

    {
        printf '## [%s] \342\200\224 %s\n\n' "$version" "$date"
        cat "$TMP_DIR/section.md"
    } > "$TMP_DIR/release.md"

    insert_at=$(awk '
        /^## \[Unreleased\]$/ { after = 1; next }
        after && /^## \[/ { print NR; exit }
    ' "$CHANGELOG")

    if [ -n "$insert_at" ]; then
        awk -v sec="$TMP_DIR/release.md" -v at="$insert_at" '
            FNR == at {
                while ((getline line < sec) > 0) print line
                close(sec)
            }
            { print }
        ' "$CHANGELOG" > "$TMP_DIR/next.md"
    else
        cat "$CHANGELOG" "$TMP_DIR/release.md" > "$TMP_DIR/next.md"
    fi
    mv "$TMP_DIR/next.md" "$CHANGELOG"
    rm -f "$FRAGMENTS"/*.md
}

case "${1:-}" in
    preview)
        preview
        ;;
    release)
        [ "$#" -eq 3 ] || usage
        # The tag is "v0.1.3" and the heading is "[0.1.3]". A maintainer reads
        # the version off the tag they are about to push, so the leading v is
        # accepted and stripped rather than rejected as a usage error.
        version=${2#v}
        case "$version" in
            ''|*[!0-9.]*) usage ;;
        esac
        [ -n "$3" ] || usage
        release "$version" "$3"
        ;;
    *)
        usage
        ;;
esac