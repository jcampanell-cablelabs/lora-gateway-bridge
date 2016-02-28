package gateway

import (
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/brocaar/loraserver"
	"github.com/brocaar/lorawan"
	. "github.com/smartystreets/goconvey/convey"
)

func TestBackend(t *testing.T) {
	Convey("Given a new Backend binding at a random port", t, func() {
		backend, err := NewBackend("127.0.0.1:0", nil, nil)
		So(err, ShouldBeNil)

		backendAddr, err := net.ResolveUDPAddr("udp", backend.conn.LocalAddr().String())
		So(err, ShouldBeNil)

		Convey("Given a fake gateway UDP publisher", func() {
			gwAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
			So(err, ShouldBeNil)
			gwConn, err := net.ListenUDP("udp", gwAddr)
			So(err, ShouldBeNil)
			defer gwConn.Close()
			So(gwConn.SetDeadline(time.Now().Add(time.Second)), ShouldBeNil)

			Convey("When sending a PULL_DATA packet", func() {
				p := PullDataPacket{
					RandomToken: 12345,
					GatewayMAC:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
				}
				b, err := p.MarshalBinary()
				So(err, ShouldBeNil)
				_, err = gwConn.WriteToUDP(b, backendAddr)
				So(err, ShouldBeNil)

				Convey("Then an ACK packet is returned", func() {
					buf := make([]byte, 65507)
					i, _, err := gwConn.ReadFromUDP(buf)
					So(err, ShouldBeNil)
					var ack PullACKPacket
					So(ack.UnmarshalBinary(buf[:i]), ShouldBeNil)
					So(ack.RandomToken, ShouldEqual, p.RandomToken)
				})
			})

			Convey("When sending a PUSH_DATA packet with stats", func() {
				p := PushDataPacket{
					RandomToken: 1234,
					GatewayMAC:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
					Payload: PushDataPayload{
						Stat: &Stat{
							Time: ExpandedTime(time.Time{}.UTC()),
							Lati: 1.234,
							Long: 2.123,
							Alti: 123,
							RXNb: 1,
							RXOK: 2,
							RXFW: 3,
							ACKR: 33.3,
							DWNb: 4,
						},
					},
				}
				b, err := p.MarshalBinary()
				So(err, ShouldBeNil)
				_, err = gwConn.WriteToUDP(b, backendAddr)
				So(err, ShouldBeNil)

				Convey("Then an ACK packet is returned", func() {
					buf := make([]byte, 65507)
					i, _, err := gwConn.ReadFromUDP(buf)
					So(err, ShouldBeNil)
					var ack PushACKPacket
					So(ack.UnmarshalBinary(buf[:i]), ShouldBeNil)
					So(ack.RandomToken, ShouldEqual, p.RandomToken)

					Convey("Then the gateway stats are returned by the stats channel", func() {
						stats := <-backend.StatsChan()
						So([8]byte(stats.MAC), ShouldEqual, [8]byte{1, 2, 3, 4, 5, 6, 7, 8})
					})
				})
			})

			Convey("When sending a PUSH_DATA packet with RXPK", func() {
				p := PushDataPacket{
					RandomToken: 1234,
					GatewayMAC:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
					Payload: PushDataPayload{
						RXPK: []RXPK{
							{
								Time: CompactTime(time.Now().UTC()),
								Tmst: 708016819,
								Freq: 868.5,
								Chan: 2,
								RFCh: 1,
								Stat: 1,
								Modu: "LORA",
								DatR: DatR{LoRa: "SF7BW125"},
								CodR: "4/5",
								RSSI: -51,
								LSNR: 7,
								Size: 16,
								Data: "QAEBAQGAAAABVfdjR6YrSw==",
							},
						},
					},
				}
				b, err := p.MarshalBinary()
				So(err, ShouldBeNil)
				_, err = gwConn.WriteToUDP(b, backendAddr)
				So(err, ShouldBeNil)

				Convey("Then an ACK packet is returned", func() {
					buf := make([]byte, 65507)
					i, _, err := gwConn.ReadFromUDP(buf)
					So(err, ShouldBeNil)
					var ack PushACKPacket
					So(ack.UnmarshalBinary(buf[:i]), ShouldBeNil)
					So(ack.RandomToken, ShouldEqual, p.RandomToken)
				})

				Convey("Then the packet is returned by the RX packet channel", func() {
					rxPacket := <-backend.RXPacketChan()

					rxPacket2, err := newRXPacketFromRXPK(p.GatewayMAC, p.Payload.RXPK[0])
					So(err, ShouldBeNil)
					So(rxPacket, ShouldResemble, rxPacket2)
				})
			})

			Convey("Given a TXPacket", func() {
				var nwkSKey lorawan.AES128Key
				macPL := lorawan.NewMACPayload(false)
				phy := lorawan.NewPHYPayload(false)
				phy.MACPayload = macPL
				phy.MHDR = lorawan.MHDR{
					MType: lorawan.UnconfirmedDataDown,
					Major: lorawan.LoRaWANR1,
				}
				So(phy.SetMIC(nwkSKey), ShouldBeNil)

				txPacket := loraserver.TXPacket{
					TXInfo: loraserver.TXInfo{
						MAC:         [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
						Immediately: true,
						Timestamp:   12345,
						Frequency:   868.1,
						Power:       14,
						DataRate: loraserver.DataRate{
							LoRa: "SF12BW125",
						},
						CodeRate: "4/5",
					},
					PHYPayload: phy,
				}

				Convey("When sending the TXPacket and the gateway is not known to the backend", func() {
					err := backend.Send(txPacket)
					Convey("Then the backend returns an error", func() {
						So(err, ShouldEqual, errGatewayDoesNotExist)
					})
				})

				Convey("When sending the TXPacket when the gateway is known to the backend", func() {
					// sending a ping should register the gateway to the backend
					p := PullDataPacket{
						RandomToken: 12345,
						GatewayMAC:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
					}
					b, err := p.MarshalBinary()
					So(err, ShouldBeNil)
					_, err = gwConn.WriteToUDP(b, backendAddr)
					So(err, ShouldBeNil)
					buf := make([]byte, 65507)
					i, _, err := gwConn.ReadFromUDP(buf)
					So(err, ShouldBeNil)
					var ack PullACKPacket
					So(ack.UnmarshalBinary(buf[:i]), ShouldBeNil)
					So(ack.RandomToken, ShouldEqual, p.RandomToken)

					err = backend.Send(txPacket)

					Convey("Then no error is returned", func() {
						So(err, ShouldBeNil)
					})

					Convey("Then the data is received by the gateway", func() {
						i, _, err := gwConn.ReadFromUDP(buf)
						So(err, ShouldBeNil)
						So(i, ShouldBeGreaterThan, 0)
						var pullResp PullRespPacket
						So(pullResp.UnmarshalBinary(buf[:i]), ShouldBeNil)

						b, err := phy.MarshalBinary()
						So(err, ShouldBeNil)

						So(pullResp, ShouldResemble, PullRespPacket{
							Payload: PullRespPayload{
								TXPK: TXPK{
									Imme: true,
									Tmst: 12345,
									Freq: 868.1,
									Powe: 14,
									Modu: "LORA",
									DatR: DatR{
										LoRa: "SF12BW125",
									},
									CodR: "4/5",
									Size: uint16(len(b)),
									Data: base64.StdEncoding.EncodeToString(b),
									IPol: true,
								},
							},
						})
					})
				})
			})
		})
	})
}

func TestNewGatewayStatPacket(t *testing.T) {
	Convey("Given a (Semtech) Stat struct and gateway MAC", t, func() {
		now := time.Now().UTC()
		stat := Stat{
			Time: ExpandedTime(now),
			Lati: 1.234,
			Long: 2.123,
			Alti: 234,
			RXNb: 1,
			RXOK: 2,
			RXFW: 3,
			ACKR: 33.3,
			DWNb: 4,
		}
		mac := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

		Convey("When calling newGatewayStatsPacket", func() {
			gw := newGatewayStatsPacket(mac, stat)
			Convey("Then all fields are set correctly", func() {
				So(gw, ShouldResemble, loraserver.GatewayStatsPacket{
					Time:                now,
					MAC:                 mac,
					Latitude:            1.234,
					Longitude:           2.123,
					Altitude:            234,
					RXPacketsReceived:   1,
					RXPacketsReceivedOK: 2,
				})
			})
		})

	})
}

func TestNewRXPacketFromRXPK(t *testing.T) {
	Convey("Given a (Semtech) RXPK and gateway MAC", t, func() {
		now := time.Now().UTC()
		rxpk := RXPK{
			Time: CompactTime(now),
			Tmst: 708016819,
			Freq: 868.5,
			Chan: 2,
			RFCh: 1,
			Stat: 1,
			Modu: "LORA",
			DatR: DatR{LoRa: "SF7BW125"},
			CodR: "4/5",
			RSSI: -51,
			LSNR: 7,
			Size: 16,
			Data: "QAEBAQGAAAABVfdjR6YrSw==",
		}
		mac := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

		Convey("When calling newRXPacketFromRXPK(", func() {
			rxPacket, err := newRXPacketFromRXPK(mac, rxpk)
			So(err, ShouldBeNil)

			Convey("Then all fields are set correctly", func() {
				b, err := base64.StdEncoding.DecodeString(rxpk.Data)
				So(err, ShouldBeNil)

				phy := lorawan.NewPHYPayload(true)
				So(phy.UnmarshalBinary(b), ShouldBeNil)

				So(rxPacket.PHYPayload, ShouldResemble, phy)

				So(rxPacket.RXInfo, ShouldResemble, loraserver.RXInfo{
					MAC:        mac,
					Time:       now,
					Timestamp:  708016819,
					Frequency:  868.5,
					Channel:    2,
					RFChain:    1,
					CRCStatus:  1,
					Modulation: "LORA",
					DataRate:   loraserver.DataRate{LoRa: "SF7BW125"},
					CodeRate:   "4/5",
					RSSI:       -51,
					LoRaSNR:    7,
					Size:       16,
				})
			})
		})
	})
}

func TestGatewaysCallbacks(t *testing.T) {
	Convey("Given a new gateways registry", t, func() {
		gw := gateways{
			gateways: make(map[lorawan.EUI64]gateway),
		}

		mac := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

		Convey("Given a onNew and onDelete callback", func() {
			var onNewCalls int
			var onDeleteCalls int

			gw.onNew = func(mac lorawan.EUI64) error {
				onNewCalls = onNewCalls + 1
				return nil
			}

			gw.onDelete = func(mac lorawan.EUI64) error {
				onDeleteCalls = onDeleteCalls + 1
				return nil
			}

			Convey("When adding a new gateway", func() {
				So(gw.set(mac, gateway{}), ShouldBeNil)

				Convey("Then onNew callback is called once", func() {
					So(onNewCalls, ShouldEqual, 1)
				})

				Convey("When updating the same gateway", func() {
					So(gw.set(mac, gateway{}), ShouldBeNil)

					Convey("Then onNew has not been called", func() {
						So(onNewCalls, ShouldEqual, 1)
					})
				})

				Convey("When cleaning up the gateways", func() {
					So(gw.cleanup(), ShouldBeNil)

					Convey("Then onDelete has been called once", func() {
						So(onDeleteCalls, ShouldEqual, 1)
					})
				})
			})
		})
	})
}