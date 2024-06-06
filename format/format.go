package format

import (
	"github.com/hongweilin90/vdk/av/avutil"
	"github.com/hongweilin90/vdk/format/aac"
	"github.com/hongweilin90/vdk/format/flv"
	"github.com/hongweilin90/vdk/format/mp4"
	"github.com/hongweilin90/vdk/format/rtmp"
	"github.com/hongweilin90/vdk/format/rtsp"
	"github.com/hongweilin90/vdk/format/ts"
)

func RegisterAll() {
	avutil.DefaultHandlers.Add(mp4.Handler)
	avutil.DefaultHandlers.Add(ts.Handler)
	avutil.DefaultHandlers.Add(rtmp.Handler)
	avutil.DefaultHandlers.Add(rtsp.Handler)
	avutil.DefaultHandlers.Add(flv.Handler)
	avutil.DefaultHandlers.Add(aac.Handler)
}
