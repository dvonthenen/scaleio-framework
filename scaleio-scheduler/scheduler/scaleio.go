package scheduler

import (
	"strings"

	log "github.com/Sirupsen/logrus"

	mesos "github.com/codedellemc/scaleio-framework/scaleio-scheduler/mesos/v1"
	common "github.com/codedellemc/scaleio-framework/scaleio-scheduler/scheduler/common"
	kvstore "github.com/codedellemc/scaleio-framework/scaleio-scheduler/scheduler/kvstore"
	types "github.com/codedellemc/scaleio-framework/scaleio-scheduler/types"
)

//IsNodeAnMDMNode returns true is node is an MDM node
func IsNodeAnMDMNode(node *types.ScaleIONode) bool {
	isMDM := node.Persona == types.PersonaMdmPrimary ||
		node.Persona == types.PersonaMdmSecondary ||
		node.Persona == types.PersonaTb
	if isMDM {
		log.Debugln("Node is an MDM Node")
	} else {
		log.Debugln("Node is an Data Node")
	}
	return isMDM
}

func appendClientPrefix(key string) string {
	if strings.Contains(key, "scaleio-sdc-") {
		return key
	}
	return "scaleio-sdc-" + key
}

func appendServerPrefix(key string) string {
	if strings.Contains(key, "scaleio-sds-") {
		return key
	}
	return "scaleio-sds-" + key
}

func getAttributeByKey(attribs []*mesos.Attribute, key string) (string, error) {
	for _, attrib := range attribs {
		if attrib.GetName() == key {
			return attrib.GetText().GetValue(), nil
		}
	}

	return "", common.ErrAttributeNotFound
}

type fixprefix func(string) string

func prepareScaleIONode(store *kvstore.KvStore, offer *mesos.Offer) (*types.ScaleIONode, error) {
	persona, state, err := store.GetNodeInfo(offer.GetHostname())
	if err != nil {
		log.Errorln("Unable to find Node metadata for", offer.GetHostname())
		return nil, err
	}

	node := &types.ScaleIONode{
		AgentID:     offer.GetAgentId().GetValue(),
		TaskID:      "scaleio-" + offer.GetHostname(),
		ExecutorID:  "executor-scaleio-" + offer.GetHostname(),
		OfferID:     offer.GetId().GetValue(),
		IPAddress:   offer.GetUrl().GetAddress().GetIp(),
		Hostname:    offer.GetHostname(),
		Persona:     persona,
		State:       state,
		LastContact: 0,
		Declarative: false,
		Advertised:  false,
	}

	keys := []string{
		"scaleio-sds-domains",
		"scaleio-sdc-domains",
	}
	fixprefix := []fixprefix{
		appendServerPrefix,
		appendClientPrefix,
	}

	for i := 0; i < 2; i++ {
		//domains
		value, err := getAttributeByKey(offer.GetAttributes(), keys[i])
		if err != nil {
			log.Warnln("Attribute", keys[i], "not found")
			continue
		}

		//this means this particular node was explicitly provisioned
		node.Declarative = true

		fsDomains := strings.Split(value, ",")
		for _, fsDomain := range fsDomains {

			if node.ProvidesDomains == nil {
				node.ProvidesDomains = make(map[string]*types.ProtectionDomain)
			}
			if node.ProvidesDomains[fsDomain] == nil {
				node.ProvidesDomains[fsDomain] = &types.ProtectionDomain{
					Name:     fsDomain,
					KeyValue: make(map[string]string),
				}
			}
			nDomain := node.ProvidesDomains[fsDomain]

			//faultsets
			fsStr, err := getAttributeByKey(offer.GetAttributes(), fixprefix[i](fsDomain+"-faultsets"))
			if err != nil {
				log.Warnln("Attribute", fixprefix[i](fsDomain+"-faultsets"), "not found")
			} else {
				log.Infoln("Attribute", fixprefix[i](fsDomain+"-faultsets"), "found!")
				fsFaultSets := strings.Split(fsStr, ",")
				for _, fsFaultSet := range fsFaultSets {
					if nDomain.FaultSets == nil {
						nDomain.FaultSets = make(map[string]*types.FaultSet)
					}
					if nDomain.FaultSets[fsFaultSet] == nil {
						nDomain.FaultSets[fsFaultSet] = &types.FaultSet{
							Name:   fsFaultSet,
							SdsIps: make([]string, 0),
						}
					}
					nFaultSet := nDomain.FaultSets[fsFaultSet]

					sdsIpsStr, err := getAttributeByKey(offer.GetAttributes(), fixprefix[i](fsDomain+"-"+fsFaultSet))
					if err != nil {
						log.Warnln("Attribute", fixprefix[i](fsDomain+"-"+fsFaultSet), "not found")
						continue
					}

					fsSdsIPs := strings.Split(sdsIpsStr, ",")
					for _, fsSdsIP := range fsSdsIPs {
						nFaultSet.SdsIps = append(nFaultSet.SdsIps, fsSdsIP)
					}
				}
			}

			//pools
			poolsStr, err := getAttributeByKey(offer.GetAttributes(), fixprefix[i](fsDomain))
			if err != nil {
				log.Warnln("Attribute", fixprefix[i](fsDomain), "not found")
				continue
			}

			fsPools := strings.Split(poolsStr, ",")
			for _, fsPool := range fsPools {

				if nDomain.Pools == nil {
					nDomain.Pools = make(map[string]*types.StoragePool)
				}
				if nDomain.Pools[fsPool] == nil {
					nDomain.Pools[fsPool] = &types.StoragePool{
						Name:     fsPool,
						KeyValue: make(map[string]string),
					}
				}
				nPool := nDomain.Pools[fsPool]

				deviceStr, err := getAttributeByKey(offer.GetAttributes(), fixprefix[i](fsDomain+"-"+fsPool))
				if err != nil {
					//TODO preserve backwards compatibility, delete at some point
					deviceStr, err = getAttributeByKey(offer.GetAttributes(), fixprefix[i](fsPool))
					if err != nil {
						log.Warnln("Attribute", fixprefix[i](fsDomain+"-"+fsPool), "not found")
						continue
					}
				}

				fsDevices := strings.Split(deviceStr, ",")
				for _, fsDevice := range fsDevices {
					if nPool.Devices == nil {
						nPool.Devices = make([]string, 0)
					}
					//TODO check for device existence
					nPool.Devices = append(nPool.Devices, fsDevice)
				}
			}
		}
	}

	return node, nil
}

func (s *ScaleIOScheduler) addScaleIONode(offer *mesos.Offer) error {
	node := common.FindScaleIONodeByHostname(s.Server.State.ScaleIO.Nodes, offer.GetHostname())
	if node != nil {
		return common.ErrNodeNotFound
	}

	node, err := prepareScaleIONode(s.Store, offer)
	if err != nil {
		return err
	}

	s.Server.State.ScaleIO.Nodes = append(s.Server.State.ScaleIO.Nodes, node)
	return nil
}
