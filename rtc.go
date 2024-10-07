package senpai

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"golang.org/x/net/context"

	"git.sr.ht/~delthas/senpai/irc"
)

func receive(track *webrtc.TrackRemote) error {
	codec := track.Codec().RTPCodecCapability
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, "ffplay", "-loglevel", "warning", "-nodisp", "-f", "ogg", "-i", "-", "-filter:a", "speechnorm=e=6.25:r=0.00001:l=1")
	w, err := c.StdinPipe()
	if err != nil {
		return err
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return err
	}
	defer func() {
		cancel()
		c.Wait()
	}()
	ow, err := oggwriter.NewWith(w, codec.ClockRate, codec.Channels)
	if err != nil {
		return err
	}
	defer ow.Close()

	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return err
		}
		if err := ow.WriteRTP(packet); err != nil {
			return err
		}
	}
}

func send(track *webrtc.TrackLocalStaticSample) error {
	ctx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "warning", "-f", "pulse", "-i", "default", "-sample_rate", "48000", "-channels", "1", "-c:a", "libopus", "-b:a", "40k", "-application", "voip", "-frame_duration", "20", "-packet_loss", "10", "-fec", "on", "-filter:a", "afftdn=nr=10:nf=-50:tn=1,speechnorm=e=6.25:r=0.00001:l=1", "-f", "ogg", "-page_duration", "1", "-")
	r, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return err
	}
	defer func() {
		cancel()
		c.Wait()
	}()
	or, _, err := oggreader.NewWith(r)
	if err != nil {
		return err
	}

	for {
		packet, _, err := or.ParseNextPage()
		if err != nil {
			return err
		}
		if err := track.WriteSample(media.Sample{
			Data:     packet,
			Duration: 20 * time.Millisecond,
		}); err != nil {
			return err
		}
	}
}

func RTC(doSend bool) (in chan irc.Event, out chan irc.Event, err error) {
	in = make(chan irc.Event, 16)
	out = make(chan irc.Event, 16)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyNegotiate,
	}

	var m webrtc.MediaEngine
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    1,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, nil, err
	}

	initialized := false

	peerConnection, err := webrtc.NewAPI(webrtc.WithMediaEngine(&m)).NewPeerConnection(config)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if !initialized {
			peerConnection.Close()
		}
	}()

	audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  "audio/opus",
		ClockRate: 48000,
		Channels:  1,
	}, "audio", "audio")
	if err != nil {
		return nil, nil, err
	}
	_, err = peerConnection.AddTrack(audioTrack)
	if err != nil {
		return nil, nil, err
	}

	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			go func() {
				if err := send(audioTrack); err != nil {
					log.Printf("send: %v", err)
				}
			}()
		}
	})

	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			log.Println("Peer Connection has gone to failed exiting")
		}

		if s == webrtc.PeerConnectionStateClosed {
			// PeerConnection was explicitly closed. This usually happens from a DTLS CloseNotify
			log.Println("Peer Connection has gone to closed exiting")
		}
	})

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		if track.Codec().RTPCodecCapability.MimeType != "audio/opus" {
			return
		}
		if err := receive(track); err != nil {
			log.Printf("receive: %v", err)
		}
	})

	if doSend {
		offer, err := peerConnection.CreateOffer(&webrtc.OfferOptions{
			OfferAnswerOptions: webrtc.OfferAnswerOptions{
				VoiceActivityDetection: true,
			},
		})
		if err != nil {
			return nil, nil, err
		}
		// We do not trickle ICE candidates here, because unsollicited call requests should not send
		// out too many messages. Preventing trickling here means we only send 1 message for the call request.
		gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
		if err := peerConnection.SetLocalDescription(offer); err != nil {
			return nil, nil, err
		}
		go func() {
			<-gatherComplete
			b, err := json.Marshal(peerConnection.LocalDescription())
			if err != nil {
				log.Printf("rtc: %v", err)
				return
			}
			out <- irc.WebRTCSessionEvent{
				Data: string(b),
			}
		}()
	}

	go func() {
		defer close(out)
		defer peerConnection.GracefulClose()
		for ev := range in {
			switch ev := ev.(type) {
			case irc.WebRTCSessionEvent:
				offer := webrtc.SessionDescription{}
				if err := json.Unmarshal([]byte(ev.Data), &offer); err != nil {
					log.Printf("rtc: %v", err)
					break
				}
				if err := peerConnection.SetRemoteDescription(offer); err != nil {
					log.Printf("rtc: %v", err)
					break
				}
				if !doSend {
					answer, err := peerConnection.CreateAnswer(&webrtc.AnswerOptions{
						OfferAnswerOptions: webrtc.OfferAnswerOptions{
							VoiceActivityDetection: true,
						},
					})
					if err != nil {
						log.Printf("rtc: %v", err)
						break
					}
					// TODO: Half ICE trickling (when implemented in pion/sdp and advertised)
					gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
					if err := peerConnection.SetLocalDescription(answer); err != nil {
						log.Printf("rtc: %v", err)
						break
					}
					<-gatherComplete
					b, err := json.Marshal(peerConnection.LocalDescription())
					if err != nil {
						log.Printf("rtc: %v", err)
						break
					}
					out <- irc.WebRTCSessionEvent{
						Data: string(b),
					}
				}
			case irc.WebRTCICECandidateEvent:
				var candidate webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(ev.Data), &candidate); err != nil {
					log.Printf("rtc: %v", err)
					break
				}
				if err := peerConnection.AddICECandidate(candidate); err != nil {
					log.Printf("rtc: %v", err)
					break
				}
			}
		}
	}()

	initialized = true
	return in, out, nil
}
