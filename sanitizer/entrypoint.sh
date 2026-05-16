#!/bin/sh
set -e

case "$1" in
    image)
        convert - -strip "jpg:-"
        ;;
    image-thumb)
        convert - -strip -resize 250x250 "jpg:-"
        ;;
    video)
        ffmpeg -i pipe:0 -c:v libvpx-vp9 -c:a libopus -f webm pipe:1
        ;;
    video-thumb)
        ffmpeg -i pipe:0 -ss 5 -vframes 1 -f image2 pipe:1
        ;;
    pdf)
        gs -q -dNOPAUSE -dBATCH -dSAFER -sDEVICE=pdfwrite \
           -sOutputFile=- -
        ;;
    pdf-thumb)
        gs -q -dNOPAUSE -dBATCH -dSAFER -sDEVICE=jpeg -dFirstPage=1 -dLastPage=1 \
           -sOutputFile=- - | convert - -resize 250x250 "jpg:-"
        ;;
    *)
        echo "unknown tool: $1" >&2
        exit 1
        ;;
esac
