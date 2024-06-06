package mjpeg

import "github.com/hongweilin90/vdk/av"

type CodecData struct {
}

func (d CodecData) Type() av.CodecType {
	return av.MJPEG
}
