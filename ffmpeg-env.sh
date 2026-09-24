# Source from the project root before compiling packages that use go-astiav.
export CGO_ENABLED=1
export PKG_CONFIG_PATH="$PWD/.cache/ffmpeg/lib/pkgconfig"
export CGO_LDFLAGS="$(pkg-config --static --libs libavcodec libavdevice libavfilter libavformat libswresample libswscale libavutil)"
