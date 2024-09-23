package webrtc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/hongweilin90/vdk/codec/h264parser"

	"github.com/hongweilin90/vdk/av"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	ErrorNotFound          = errors.New("WebRTC Stream Not Found")
	ErrorCodecNotSupported = errors.New("WebRTC Codec Not Supported")
	ErrorClientOffline     = errors.New("WebRTC Client Offline")
	ErrorNotTrackAvailable = errors.New("WebRTC Not Track Available")
	ErrorIgnoreAudioTrack  = errors.New("WebRTC Ignore Audio Track codec not supported WebRTC support only PCM_ALAW or PCM_MULAW")
)

type Muxer struct {
	streams                map[int8]*Stream
	status                 webrtc.ICEConnectionState
	stop                   bool
	pc                     *webrtc.PeerConnection
	pd                     chan []byte
	candidateChangedNotify func([]byte)
	closeCallback          func()
	stopCh                 chan bool
	ClientACK              *time.Timer
	StreamACK              *time.Timer
	Options                Options
}
type Stream struct {
	codec av.CodecData
	track *webrtc.TrackLocalStaticSample
}
type Options struct {
	// ICEServers is a required array of ICE server URLs to connect to (e.g., STUN or TURN server URLs)
	ICEServers []string
	// ICEUsername is an optional username for authenticating with the given ICEServers
	ICEUsername string
	// ICECredential is an optional credential (i.e., password) for authenticating with the given ICEServers
	ICECredential string
	// ICECandidates sets a list of external IP addresses of 1:1
	ICECandidates []string
	// PortMin is an optional minimum (inclusive) ephemeral UDP port range for the ICEServers connections
	PortMin uint16
	// PortMin is an optional maximum (inclusive) ephemeral UDP port range for the ICEServers connections
	PortMax uint16
}

func NewMuxer(options Options, candidateChangedNotify func([]byte), closeCallback func()) *Muxer {
	tmp := Muxer{
		Options:                options,
		pd:                     make(chan []byte, 100),
		candidateChangedNotify: candidateChangedNotify,
		closeCallback:          closeCallback,
		stopCh:                 make(chan bool),
		ClientACK:              time.NewTimer(time.Second * 20),
		StreamACK:              time.NewTimer(time.Second * 20),
		streams:                make(map[int8]*Stream)}
	go tmp.transmittingCandidate()
	return &tmp
}
func (element *Muxer) NewPeerConnection(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
	if len(element.Options.ICEServers) > 0 {
		log.Println("Set ICEServers", element.Options.ICEServers)
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs:           element.Options.ICEServers,
			Username:       element.Options.ICEUsername,
			Credential:     element.Options.ICECredential,
			CredentialType: webrtc.ICECredentialTypePassword,
		})
	} else {
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs: []string{"stun:stun.l.google.com:19302"},
		})
	}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		log.Println("Failed in m.RegisterDefaultCodecs()", err.Error())
		return nil, err
	}
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		log.Println("Failed in webrtc.RegisterDefaultInterceptors(m, i)", err.Error())
		return nil, err
	}
	s := webrtc.SettingEngine{}
	if element.Options.PortMin > 0 && element.Options.PortMax > 0 && element.Options.PortMax > element.Options.PortMin {
		s.SetEphemeralUDPPortRange(element.Options.PortMin, element.Options.PortMax)
		log.Println("Set UDP ports to", element.Options.PortMin, "..", element.Options.PortMax)
	}
	if len(element.Options.ICECandidates) > 0 {
		s.SetNAT1To1IPs(element.Options.ICECandidates, webrtc.ICECandidateTypeHost)
		log.Println("Set ICECandidates", element.Options.ICECandidates)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i), webrtc.WithSettingEngine(s))
	p, err := api.NewPeerConnection(configuration)
	if err != nil {
		log.Println("Failed in api.NewPeerConnection(configuration)", err.Error())
	}
	return p, err
}
func (element *Muxer) WriteHeader(streams []av.CodecData, sdp64 string) (string, error) {
	var WriteHeaderSuccess bool
	if len(streams) == 0 {
		return "", ErrorNotFound
	}
	sdpB, err := base64.StdEncoding.DecodeString(sdp64)
	if err != nil {
		return "", err
	}
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sdpB),
	}
	peerConnection, err := element.NewPeerConnection(webrtc.Configuration{
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	})
	if err != nil {
		log.Println("Failed in element.NewPeerConnection", err.Error())
		return "", err
	}
	defer func() {
		if !WriteHeaderSuccess {
			err = element.Close()
			if err != nil {
				log.Println("Failed in element.Close()", err.Error())
				log.Println(err)
			}
		}
	}()
	for i, i2 := range streams {
		var track *webrtc.TrackLocalStaticSample
		if i2.Type().IsVideo() {
			if i2.Type() == av.H264 {
				track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
					MimeType: webrtc.MimeTypeH264,
				}, "pion-rtsp-video", "pion-video")
				if err != nil {
					log.Println("Failed in webrtc.NewTrackLocalStaticSample", err.Error())
					return "", err
				}
				if rtpSender, err := peerConnection.AddTrack(track); err != nil {
					log.Println("Failed in peerConnection.AddTrack", err.Error())
					return "", err
				} else {
					go func() {
						rtcpBuf := make([]byte, 1500)
						for {
							if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
								return
							}
						}
					}()
				}
			}
		} else if i2.Type().IsAudio() {
			AudioCodecString := webrtc.MimeTypePCMA
			switch i2.Type() {
			case av.PCM_ALAW:
				AudioCodecString = webrtc.MimeTypePCMA
			case av.PCM_MULAW:
				AudioCodecString = webrtc.MimeTypePCMU
			case av.OPUS:
				AudioCodecString = webrtc.MimeTypeOpus
			default:
				log.Println(ErrorIgnoreAudioTrack)
				continue
			}
			track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
				MimeType:  AudioCodecString,
				Channels:  uint16(i2.(av.AudioCodecData).ChannelLayout().Count()),
				ClockRate: uint32(i2.(av.AudioCodecData).SampleRate()),
			}, "pion-rtsp-audio", "pion-rtsp-audio")
			if err != nil {
				log.Println("Failed in webrtc.NewTrackLocalStaticSample", err.Error())
				return "", err
			}
			if rtpSender, err := peerConnection.AddTrack(track); err != nil {
				log.Println("Failed in peerConnection.AddTrack(track)", err.Error())
				return "", err
			} else {
				go func() {
					rtcpBuf := make([]byte, 1500)
					for {
						if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
							return
						}
					}
				}()
			}
		}
		element.streams[int8(i)] = &Stream{track: track, codec: i2}
	}
	if len(element.streams) == 0 {
		log.Println(ErrorNotTrackAvailable)
		return "", ErrorNotTrackAvailable
	}
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		element.status = connectionState
		if connectionState == webrtc.ICEConnectionStateDisconnected {
			element.Close()
		}
		if connectionState == webrtc.ICEConnectionStateClosed {
			element.Close()
		}
		if connectionState == webrtc.ICEConnectionStateFailed {
			element.Close()
		}
	})
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			element.ClientACK.Reset(5 * time.Second)
		})
	})

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		log.Println("Failed in peerConnection.SetRemoteDescription(offer)", err.Error())
		return "", err
	}
	gatherCompletePromise := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Println("Failed in peerConnection.CreateAnswer(nil)", err.Error())
		return "", err
	}
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		log.Println("Failed in peerConnection.SetLocalDescription(answer)", err.Error())
		return "", err
	}
	//
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			data, err := json.Marshal(candidate.ToJSON())
			if err == nil && data != nil && element.pd != nil {
				if len(element.pd) < cap(element.pd) {
					element.pd <- data
				}
			} else {
				log.Printf("error from OnICECandidate:%s", err.Error())
			}
		}
	})
	element.pc = peerConnection
	waitT := time.NewTimer(time.Second * 10)
	select {
	case <-waitT.C:
		log.Println("gatherCompletePromise wait")
		return "", errors.New("gatherCompletePromise wait")
	case <-gatherCompletePromise:
		//Connected
	}
	resp := peerConnection.LocalDescription()
	WriteHeaderSuccess = true
	return base64.StdEncoding.EncodeToString([]byte(resp.SDP)), nil

}

func (element *Muxer) WritePacket(pkt av.Packet) (err error) {
	//log.Println("WritePacket", pkt.Time, element.stop, webrtc.ICEConnectionStateConnected, pkt.Idx, element.streams[pkt.Idx])
	var WritePacketSuccess bool
	defer func() {
		if !WritePacketSuccess {
			element.Close()
		}
	}()
	if element.stop {
		log.Println(ErrorClientOffline)
		return ErrorClientOffline
	}
	if element.status == webrtc.ICEConnectionStateChecking {
		WritePacketSuccess = true
		return nil
	}
	if element.status != webrtc.ICEConnectionStateConnected {
		return nil
	}
	if tmp, ok := element.streams[pkt.Idx]; ok {
		element.StreamACK.Reset(10 * time.Second)
		if len(pkt.Data) < 5 {
			return nil
		}
		switch tmp.codec.Type() {
		case av.H264:
			nalus, _ := h264parser.SplitNALUs(pkt.Data)
			for _, nalu := range nalus {
				naltype := nalu[0] & 0x1f
				if naltype == 5 {
					codec := tmp.codec.(h264parser.CodecData)
					err = tmp.track.WriteSample(media.Sample{Data: append([]byte{0, 0, 0, 1}, bytes.Join([][]byte{codec.SPS(), codec.PPS(), nalu}, []byte{0, 0, 0, 1})...), Duration: pkt.Duration})
				} else {
					err = tmp.track.WriteSample(media.Sample{Data: append([]byte{0, 0, 0, 1}, nalu...), Duration: pkt.Duration})
				}
				if err != nil {
					log.Println("Failed in tmp.track.WriteSample", err.Error())
					return err
				}
			}
			WritePacketSuccess = true
			return
			/*

				if pkt.IsKeyFrame {
					pkt.Data = append([]byte{0, 0, 0, 1}, bytes.Join([][]byte{codec.SPS(), codec.PPS(), pkt.Data[4:]}, []byte{0, 0, 0, 1})...)
				} else {
					pkt.Data = pkt.Data[4:]
				}

			*/
		case av.PCM_ALAW:
		case av.OPUS:
		case av.PCM_MULAW:
		case av.AAC:
			//TODO: NEED ADD DECODER AND ENCODER
			return ErrorCodecNotSupported
		case av.PCM:
			//TODO: NEED ADD ENCODER
			return ErrorCodecNotSupported
		default:
			return ErrorCodecNotSupported
		}
		err = tmp.track.WriteSample(media.Sample{Data: pkt.Data, Duration: pkt.Duration})
		if err == nil {
			WritePacketSuccess = true
		}
		log.Println("Failed in tmp.track.WriteSample - 2", err.Error())
		return err
	} else {
		WritePacketSuccess = true
		return nil
	}
}

func (element *Muxer) transmittingCandidate() {
	for {
		select {
		case <-element.stopCh:
			log.Println("Received stop command")
			return
		case data := <-element.pd:
			log.Println("Received candidate and notified peer client")
			element.candidateChangedNotify(data)
		}
	}
}

func (element *Muxer) AddIceCandidate(candidate []byte) error {
	if candidate != nil {
		var iceCandidate webrtc.ICECandidateInit
		err := json.Unmarshal(candidate, &iceCandidate)
		if err != nil {
			return err
		}
		err = element.pc.AddICECandidate(iceCandidate)
		if err != nil {
			log.Println("failed in adding ice candidate", err.Error())
		} else {
			log.Println("succeed in adding ice candidate")
		}
		return err
	} else {
		log.Println("candidate is nil")
		return errors.New("candidate is nil")
	}
}

func (element *Muxer) Close() error {
	element.stop = true
	if element.pc != nil {
		err := element.pc.Close()
		if err != nil {
			return err
		}
	}
	if element.closeCallback != nil {
		element.closeCallback()
	}
	element.stopCh <- true
	return nil
}
