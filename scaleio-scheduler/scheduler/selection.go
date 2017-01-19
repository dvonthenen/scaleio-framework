package scheduler

import (
	"errors"

	log "github.com/Sirupsen/logrus"

	mesos "github.com/codedellemc/scaleio-framework/scaleio-scheduler/mesos/v1"
	common "github.com/codedellemc/scaleio-framework/scaleio-scheduler/scheduler/common"
	types "github.com/codedellemc/scaleio-framework/scaleio-scheduler/types"
)

/*
It looks like multiple IPs (either virtual or multiple NICs) doesnt make a difference
when making SDSs. Once an SDS (hostname is resolved) is added to ScaleIO,
that entire node is dedicated to that Protection Domain.

The consequence is that you can either have a single NIC set to "all" or
1 NIC = "sdsOnly" and 1 NIC "sdcOnly". Meaning PD, SP and SDS is actually
a tuple despite what the API could lead you to believe.
*/

/*
Metadata structure on disk for the Mesos Agent:

/etc/mesos-slave/attributes/<attribute name>
    protection domain
    storage pool - 1 to many devices

scaleio-sds-domains = domain1,domain2
scaleio-sds-domain1 = pool1,pool2
scaleio-sds-domain1-faultsets = set1 (out of set1, set2)
scaleio-sds-domain1-set1 = 10.0.0.10,10.0.1.10
scaleio-sds-domain2 = pool3
scaleio-sds-domain1-pool1 = /dev/vxdf,/dev/vxdg
scaleio-sds-domain1-pool2 = /dev/vxdh
scaleio-sds-domain2-faultsets = set2
scaleio-sds-domain2-set1 = 10.0.0.13
scaleio-sds-domain2-pool3 = /dev/vxdi

scaleio-sdc-domains = domain1,domain2
scaleio-sdc-domain1 = pool1
scaleio-sdc-domain2 = pool3


Metadata structure in the KeyValue Store:

scaleio-framework/<framework role>
	version = 1
	/configuration
		configured = true
		primary = "10.0.0.10"
		secondary = "10.0.0.11"
		tiebreaker = "10.0.0.12"
	/nodes
		/10.0.0.10
			persona = 1
			state = 2, 3, etc
			domainlist = domain1,domain2
			/domains
				/domain1
					sdslist = 10.0.0.10,10.0.1.10
					/sdss
						/10.0.0.10
							type = sds_only
							faultset  = set1
						/10.0.1.10
							type = sdc_only
					poollist = pool1,pool2
					/pools
						pool1 = /dev/xvdf,/dev/xvdg
						pool1-sds = 10.0.0.10
						pool2 = /dev/xvdh
						pool2-sds = 10.0.1.10
				/domain2
					sdslist = 10.0.0.12
					/sdss
						/s10.0.0.12
							type = all
							faultset  = set2
					faultsetlist = set2
					poollist = pool3
					/pools
						pool3 = /dev/xvdi
						pool3-sds = 10.0.0.12
		/10.0.0.11
			persona = 2
			state = 2, 3, etc
			...
		/10.0.0.12
			persona = 3
			state = 2, 3, etc
			...
		/10.0.0.13
			persona = 4
			state = 2, 3, etc
			...
*/

var (
	//ErrMdmSelectionFailed Unable to select an MDM node from available Agents
	ErrMdmSelectionFailed = errors.New("Unable to select an MDM node from available Agents")
)

func getManuallyConfigNode(offers []*mesos.Offer, persona int) *mesos.Offer {
	for _, offer := range offers {
		attribs := offer.GetAttributes()
		for _, attrib := range attribs {
			if attrib.GetName() == "scaleio-persona" &&
				attrib.GetText().GetValue() == common.PersonaIDToString(persona) {
				return offer
			}
		}
	}
	return nil
}

func (s *ScaleIOScheduler) obtainBestOfferForMdm(offers []*mesos.Offer) *mesos.Offer {
	var highestCPU float64
	var highestMem float64
	var higherOffer *mesos.Offer

	for _, offer := range offers {
		//has the node already been allocated?
		_, _, err := s.Store.GetNodeInfo(offer.GetHostname())
		if err == nil {
			continue
		}

		cpuResources := filterResources(offer.Resources, func(res *mesos.Resource) bool {
			return res.GetName() == "cpus"
		})
		cpus := 0.0
		for _, res := range cpuResources {
			cpus += res.GetScalar().GetValue()
		}

		memResources := filterResources(offer.Resources, func(res *mesos.Resource) bool {
			return res.GetName() == "mem"
		})
		mems := 0.0
		for _, res := range memResources {
			mems += res.GetScalar().GetValue()
		}

		if s.Config.ExecutorMdmCPU >= (cpus*s.Config.ExecutorCPUFactor) ||
			s.Config.ExecutorMdmMemory >= (mems*s.Config.ExecutorMemoryFactor) {
			continue
		}

		if cpus > (highestCPU*.75) && mems > (highestMem*1.25) {
			higherOffer = offer
			highestCPU = cpus
			highestMem = mems
		} else if cpus > highestCPU {
			higherOffer = offer
			highestCPU = cpus
			highestMem = mems
		}
	}

	return higherOffer
}

func (s *ScaleIOScheduler) selectAnMdmNode(offers []*mesos.Offer, nodeID string, mdmType int) error {
	node := common.FindScaleIONodeByHostname(s.Server.State.ScaleIO.Nodes, nodeID)
	if node != nil {
		log.Debugln(common.PersonaIDToString(mdmType), " MDM node exists already")
	} else if _, _, err := s.Store.GetNodeInfo(nodeID); err == nil {
		log.Debugln(common.PersonaIDToString(mdmType), " MDM metadata exists, but not in state object")

		for _, offer := range offers {
			if offer.GetHostname() == nodeID {
				log.Debugln("Found offer based on nodeID:", nodeID)
				s.addScaleIONode(offer)
				break
			}
		}
	} else if offer := getManuallyConfigNode(offers, mdmType); offer != nil {
		log.Debugln(common.PersonaIDToString(mdmType), " MDM has been manually selected")

		err := s.Store.SetNodeInfo(offer.GetHostname(), mdmType, types.StateUnknown)
		if err != nil {
			log.Debugln("Failed to set ", common.PersonaIDToString(mdmType), " MDM metadata:", err)
			return err
		}
		s.addScaleIONode(offer)
	} else {
		log.Debugln(common.PersonaIDToString(mdmType), "MDM needs to be automatically selected")
		offer := s.obtainBestOfferForMdm(offers)
		if offer == nil {
			log.Errorln("Unable to find an acceptable node to run the",
				common.PersonaIDToString(mdmType), "MDM node")
			return ErrMdmSelectionFailed
		}

		err := s.Store.SetNodeInfo(offer.GetHostname(), mdmType, types.StateUnknown)
		if err != nil {
			log.Debugln("Failed to set ", common.PersonaIDToString(mdmType), " MDM metadata:", err)
			return err
		}
		s.addScaleIONode(offer)
	}

	return nil
}

func (s *ScaleIOScheduler) selectDataNode(offer *mesos.Offer) error {
	_, _, err := s.Store.GetNodeInfo(offer.GetHostname())
	if err != nil {
		err = s.Store.SetNodeInfo(offer.GetHostname(), types.PersonaNode, types.StateUnknown)
		if err != nil {
			log.Debugln("Failed to set data node metadata:", err)
			return err
		}
	}
	s.addScaleIONode(offer)

	return nil
}

func (s *ScaleIOScheduler) performNodeSelection(offers []*mesos.Offer) error {
	log.Debugln("performNodeSelection ENTER")

	if s.Config.PrimaryMdmAddress == "" &&
		s.Config.SecondaryMdmAddress == "" &&
		s.Config.TieBreakerMdmAddress == "" {
		pri, sec, tb := s.Store.GetMdmNodes()
		if len(pri) == 0 {
			log.Debugln("The Primary MDM node has not been selected")
		} else {
			log.Debugln("The Primary MDM node has been selected:", pri)
		}
		err := s.selectAnMdmNode(offers, pri, types.PersonaMdmPrimary)
		if err != nil {
			log.Errorln("Failed to select a Primary MDM node:", err)
			log.Debugln("performNodeSelection LEAVE")
			return err
		}

		if len(sec) == 0 {
			log.Debugln("The Secondary MDM node has not been selected")
		} else {
			log.Debugln("The Secondary MDM node has been selected:", sec)
		}
		err = s.selectAnMdmNode(offers, sec, types.PersonaMdmSecondary)
		if err != nil {
			log.Errorln("Failed to select a Secondary MDM node:", err)
			log.Debugln("performNodeSelection LEAVE")
			return err
		}

		if len(tb) == 0 {
			log.Debugln("The Tiebreaker MDM node has not been selected")
		} else {
			log.Debugln("The Tiebreaker MDM node has been selected:", tb)
		}
		err = s.selectAnMdmNode(offers, tb, types.PersonaTb)
		if err != nil {
			log.Errorln("Failed to select a TieBreaker MDM node:", err)
			log.Debugln("performNodeSelection LEAVE")
			return err
		}
	}

	for _, offer := range offers {
		if common.FindScaleIONodeByHostname(s.Server.State.ScaleIO.Nodes, offer.GetHostname()) != nil {
			log.Debugln("Node", offer.GetHostname(), "already has a persona")
			continue
		}

		log.Debugln("Node", offer.GetHostname(), "persona being set to DataNode")
		err := s.selectDataNode(offer)
		if err != nil {
			log.Errorln("Failed to select data node:", err)
			log.Debugln("performNodeSelection LEAVE")
			return err
		}
	}

	log.Debugln("performNodeSelection Succeeded")
	log.Debugln("performNodeSelection LEAVE")
	return nil
}
