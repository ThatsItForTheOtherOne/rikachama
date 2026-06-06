#!/bin/sh
set -e

case "$1" in
    image)
        magick - -strip "jpg:-"
        ;;
    image-png)
        magick - -strip "png:-"
        ;;
    image-gif)
        magick - -strip "gif:-"
        ;;
    image-thumb)
        magick - -strip -resize "250x250>" "jpg:-"
        ;;
    image-png-thumb)
        magick - -strip -resize "250x250>" "png:-"
        ;;
    video)
        tmp=$(mktemp /tmp/in.XXXXXX)
        out=$(mktemp /tmp/out.XXXXXX)
        trap 'rm -f "$tmp" "$out"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -c:v libx264  -preset veryfast -crf 23 -pix_fmt yuv420p -c:a aac -movflags +faststart -f mp4 -y "$out"
        cat "$out"
        ;;
    video-thumb)
        tmp=$(mktemp /tmp/in.XXXXXX)
        trap 'rm -f "$tmp"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -vf "thumbnail" -frames:v 1 -update 1 -f image2 pipe:1 \
            | magick - -strip -resize "250x250>" "jpg:-"
        ;;
    pdf)
        d=$(mktemp -d /tmp/pdf.XXXXXX)
        trap 'rm -rf "$d"' EXIT
        cat > "$d/in.pdf"
        mutool clean -g -g -g -g -d -a -s "$d/in.pdf" "$d/out.pdf"
        cat "$d/out.pdf"
        ;;
    pdf-thumb)
        d=$(mktemp -d /tmp/pdf.XXXXXX)
        trap 'rm -rf "$d"' EXIT
        cat > "$d/in.pdf"
        mutool draw -F png -h 250 -o - "$d/in.pdf" 1 \
            | magick - -strip -resize "250x250>" "jpg:-"
        ;;
    *)
        echo "unknown tool: $1" >&2
        exit 1
        ;;
esac
