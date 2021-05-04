// Copyright (c) 2020 Doc.ai and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ipcontext provides chain element for setup link ip properties
package ipcontext

import (
	"context"
	"net"
	"os"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	kernelmech "github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/kernel"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"

	"github.com/networkservicemesh/sdk-kernel/pkg/kernel"
)

type ipContextServer struct{}

// NewServer returns a new ip context server chain element
func NewServer() networkservice.NetworkServiceServer {
	return &ipContextServer{}
}

func (s *ipContextServer) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*networkservice.Connection, error) {
	if mech := kernelmech.ToMechanism(request.GetConnection().GetMechanism()); mech != nil {
		ifName := mech.GetInterfaceName(request.GetConnection())
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get net interface: %v", ifName)
		}

		if link.Attrs().OperState != netlink.OperUp {
			if err = netlink.LinkSetUp(link); err != nil {
				return nil, errors.Wrapf(err, "failed to set up net interface: %v", ifName)
			}
		}

		ipContext := request.GetConnection().GetContext().GetIpContext()
		for _, ipNet := range ipContext.GetSrcIpAddrs() {
			ipAddr, err := netlink.ParseAddr(ipNet)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid IP address: %v", ipNet)
			}
			if err := setIPAddr(ipAddr, link); err != nil {
				return nil, err
			}
		}
		if err := setRoutes(ipContext.GetSrcRoutes(), link); err != nil {
			return nil, err
		}
		if err := setIPNeighbors(ipContext.GetIpNeighbors(), link); err != nil {
			return nil, err
		}
	}
	return next.Server(ctx).Request(ctx, request)
}

func setIPAddr(ipAddr *netlink.Addr, link netlink.Link) error {
	ipAddrs, err := netlink.AddrList(link, kernel.FamilyAll)
	if err != nil {
		return errors.Wrapf(err, "failed to get the net interface IP addresses: %v", link.Attrs().Name)
	}

	for i := range ipAddrs {
		if ipAddr.Equal(ipAddrs[i]) {
			return nil
		}
	}

	if err := netlink.AddrAdd(link, ipAddr); err != nil {
		return errors.Wrapf(err, "failed to add IP address to the net interface: %v %v", link.Attrs().Name, ipAddr)
	}

	return nil
}

func setRoutes(routes []*networkservice.Route, link netlink.Link) error {
	for _, route := range routes {
		_, routeNet, err := net.ParseCIDR(route.GetPrefix())
		if err != nil {
			return errors.Wrapf(err, "invalid route CIDR: %v", route.GetPrefix())
		}
		kernelRoute := &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst: &net.IPNet{
				IP:   routeNet.IP,
				Mask: routeNet.Mask,
			},
		}
		if route.GetNextHopIP() != nil {
			kernelRoute.Gw = route.GetNextHopIP()
		}
		if err = netlink.RouteAdd(kernelRoute); err != nil && !os.IsExist(err) {
			return errors.Wrapf(err, "failed to add route: %v", route.GetPrefix())
		}
	}
	return nil
}

func setIPNeighbors(ipNeighbours []*networkservice.IpNeighbor, link netlink.Link) error {
	for _, ipNeighbor := range ipNeighbours {
		macAddr, err := net.ParseMAC(ipNeighbor.HardwareAddress)
		if err != nil {
			return errors.Wrapf(err, "invalid neighbor MAC address: %v", ipNeighbor.HardwareAddress)
		}
		if err := netlink.NeighAdd(&netlink.Neigh{
			LinkIndex:    link.Attrs().Index,
			State:        kernel.NudReachable,
			IP:           net.ParseIP(ipNeighbor.Ip),
			HardwareAddr: macAddr,
		}); err != nil && !os.IsExist(err) {
			return errors.Wrapf(err, "failed to add IP neighbor: %v", ipNeighbor)
		}
	}
	return nil
}

func (s *ipContextServer) Close(ctx context.Context, conn *networkservice.Connection) (*empty.Empty, error) {
	return next.Server(ctx).Close(ctx, conn)
}
