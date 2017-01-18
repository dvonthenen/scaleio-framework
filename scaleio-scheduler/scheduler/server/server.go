package server

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	goscaleio "github.com/codedellemc/goscaleio"
	negroni "github.com/codegangsta/negroni"
	"github.com/gorilla/mux"

	config "github.com/codedellemc/scaleio-framework/scaleio-scheduler/config"
	common "github.com/codedellemc/scaleio-framework/scaleio-scheduler/scheduler/common"
	kvstore "github.com/codedellemc/scaleio-framework/scaleio-scheduler/scheduler/kvstore"
	types "github.com/codedellemc/scaleio-framework/scaleio-scheduler/types"
)

const (
	rootKey = "scaleio-framework"
)

//RestServer representation for a REST API server
type RestServer struct {
	Config *config.Config
	Store  *kvstore.KvStore
	Server *negroni.Negroni
	State  *types.ScaleIOFramework
	Index  int

	sync.Mutex
}

//NewRestServer generates a new REST API server
func NewRestServer(cfg *config.Config, store *kvstore.KvStore) *RestServer {
	preconfig := cfg.PrimaryMdmAddress != "" && cfg.SecondaryMdmAddress != "" &&
		cfg.TieBreakerMdmAddress != ""

	scaleio := &types.ScaleIOFramework{
		SchedulerAddress: fmt.Sprintf("http://%s:%d", cfg.RestAddress, cfg.RestPort),
		LogLevel:         cfg.LogLevel,
		Debug:            cfg.Debug,
		Experimental:     cfg.Experimental,
		KeyValue:         make(map[string]string),
		ScaleIO: &types.ScaleIOConfig{
			Configured:       store.GetConfigured(),
			ClusterID:        cfg.ClusterID,
			ClusterName:      cfg.ClusterName,
			LbGateway:        cfg.LbGateway,
			ProtectionDomain: cfg.ProtectionDomain,
			StoragePool:      cfg.StoragePool,
			AdminPassword:    cfg.AdminPassword,
			APIVersion:       cfg.APIVersion,
			KeyValue:         make(map[string]string),
			Preconfig: types.ScaleIOPreConfig{
				PreConfigEnabled:     preconfig,
				PrimaryMdmAddress:    cfg.PrimaryMdmAddress,
				SecondaryMdmAddress:  cfg.SecondaryMdmAddress,
				TieBreakerMdmAddress: cfg.TieBreakerMdmAddress,
				GatewayAddress:       cfg.GatewayAddress,
			},
			Ubuntu14: types.Ubuntu14Packages{
				Mdm: cfg.DebMdm,
				Sds: cfg.DebSds,
				Sdc: cfg.DebSdc,
				Lia: cfg.DebLia,
				Gw:  cfg.DebGw,
			},
			Rhel7: types.Rhel7Packages{
				Mdm: cfg.RpmMdm,
				Sds: cfg.RpmSds,
				Sdc: cfg.RpmSdc,
				Lia: cfg.RpmLia,
				Gw:  cfg.RpmGw,
			},
		},
		Rexray: types.RexrayConfig{
			Branch:  cfg.RexrayBranch,
			Version: cfg.RexrayVersion,
		},
		Isolator: types.IsolatorConfig{
			Binary: cfg.IsolatorBinary,
		},
	}

	restServer := &RestServer{
		Config: cfg,
		Store:  store,
		State:  scaleio,
		Index:  1,
	}

	mux := mux.NewRouter()
	mux.HandleFunc("/scaleio-executor", func(w http.ResponseWriter, r *http.Request) {
		downloadExecutor(w, r, restServer)
	}).Methods("GET")
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		getVersion(w, r, restServer)
	}).Methods("GET")
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		getState(w, r, restServer)
	}).Methods("GET")
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		setState(w, r, restServer)
	}).Methods("POST")
	mux.HandleFunc("/api/node/state", func(w http.ResponseWriter, r *http.Request) {
		setNodeState(w, r, restServer)
	}).Methods("POST")
	mux.HandleFunc("/api/node/device", func(w http.ResponseWriter, r *http.Request) {
		setNodeDevices(w, r, restServer)
	}).Methods("POST")
	mux.HandleFunc("/api/node/ping", func(w http.ResponseWriter, r *http.Request) {
		setNodePing(w, r, restServer)
	}).Methods("POST")
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		displayState(w, r, restServer)
	}).Methods("GET")
	//TODO delete this below when a real UI is embedded
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		displayState(w, r, restServer)
	}).Methods("GET")
	server := negroni.Classic()
	server.UseHandler(mux)

	//Run is a blocking call for Negroni... so go routine it
	go func() {
		server.Run(cfg.RestAddress + ":" + strconv.Itoa(cfg.RestPort))
	}()

	restServer.Server = server

	//MonitorForState watch for state changes
	go func() {
		err := restServer.MonitorForState()
		if err != nil {
			log.Errorln("MonitorForState:", err)
		}
	}()

	return restServer
}

func cloneState(src *types.ScaleIOFramework) *types.ScaleIOFramework {
	dst := &types.ScaleIOFramework{}

	dst.Debug = src.Debug
	dst.Experimental = src.Experimental
	dst.LogLevel = src.LogLevel
	dst.Rexray.Branch = src.Rexray.Branch
	dst.Rexray.Version = src.Rexray.Version
	dst.SchedulerAddress = src.SchedulerAddress
	dst.Isolator.Binary = src.Isolator.Binary
	dst.KeyValue = make(map[string]string)
	for key, val := range src.KeyValue {
		dst.KeyValue[key] = val
	}

	dst.ScaleIO = &types.ScaleIOConfig{}
	dst.ScaleIO.AdminPassword = src.ScaleIO.AdminPassword
	dst.ScaleIO.APIVersion = src.ScaleIO.APIVersion
	dst.ScaleIO.ClusterID = src.ScaleIO.ClusterID
	dst.ScaleIO.ClusterName = src.ScaleIO.ClusterName
	dst.ScaleIO.Configured = src.ScaleIO.Configured
	dst.ScaleIO.LbGateway = src.ScaleIO.LbGateway
	dst.ScaleIO.Preconfig.GatewayAddress = src.ScaleIO.Preconfig.GatewayAddress
	dst.ScaleIO.Preconfig.PreConfigEnabled = src.ScaleIO.Preconfig.PreConfigEnabled
	dst.ScaleIO.Preconfig.PrimaryMdmAddress = src.ScaleIO.Preconfig.PrimaryMdmAddress
	dst.ScaleIO.Preconfig.SecondaryMdmAddress = src.ScaleIO.Preconfig.SecondaryMdmAddress
	dst.ScaleIO.Preconfig.TieBreakerMdmAddress = src.ScaleIO.Preconfig.TieBreakerMdmAddress
	dst.ScaleIO.ProtectionDomain = src.ScaleIO.ProtectionDomain
	dst.ScaleIO.Rhel7.Gw = src.ScaleIO.Rhel7.Gw
	dst.ScaleIO.Rhel7.Lia = src.ScaleIO.Rhel7.Lia
	dst.ScaleIO.Rhel7.Mdm = src.ScaleIO.Rhel7.Mdm
	dst.ScaleIO.Rhel7.Sdc = src.ScaleIO.Rhel7.Sdc
	dst.ScaleIO.Rhel7.Sds = src.ScaleIO.Rhel7.Sds
	dst.ScaleIO.StoragePool = src.ScaleIO.StoragePool
	dst.ScaleIO.Ubuntu14.Gw = src.ScaleIO.Ubuntu14.Gw
	dst.ScaleIO.Ubuntu14.Lia = src.ScaleIO.Ubuntu14.Lia
	dst.ScaleIO.Ubuntu14.Mdm = src.ScaleIO.Ubuntu14.Mdm
	dst.ScaleIO.Ubuntu14.Sdc = src.ScaleIO.Ubuntu14.Sdc
	dst.ScaleIO.Ubuntu14.Sds = src.ScaleIO.Ubuntu14.Sds
	dst.ScaleIO.KeyValue = make(map[string]string)
	dst.ScaleIO.Nodes = make([]*types.ScaleIONode, 0)
	for key, val := range src.ScaleIO.KeyValue {
		dst.ScaleIO.KeyValue[key] = val
	}

	for _, node := range src.ScaleIO.Nodes {
		dstNode := &types.ScaleIONode{
			AgentID:         node.AgentID,
			TaskID:          node.TaskID,
			ExecutorID:      node.ExecutorID,
			OfferID:         node.OfferID,
			IPAddress:       node.IPAddress,
			Hostname:        node.Hostname,
			Persona:         node.Persona,
			State:           node.State,
			LastContact:     node.LastContact,
			Declarative:     node.Declarative,
			Advertised:      node.Advertised,
			KeyValue:        make(map[string]string),
			ProvidesDomains: make(map[string]*types.ProtectionDomain),
			ConsumesDomains: make(map[string]*types.ProtectionDomain),
		}
		for key, val := range node.KeyValue {
			dstNode.KeyValue[key] = val
		}
		for keyDomain, pDomain := range node.ProvidesDomains {
			dstPDomain := &types.ProtectionDomain{
				Name:     pDomain.Name,
				KeyValue: make(map[string]string),
				Pools:    make(map[string]*types.StoragePool),
			}
			for key, val := range pDomain.KeyValue {
				dstPDomain.KeyValue[key] = val
			}
			for keyPool, pPool := range pDomain.Pools {
				dstPool := &types.StoragePool{
					Name:     pPool.Name,
					Devices:  make([]string, 0),
					KeyValue: make(map[string]string),
				}
				for _, device := range pPool.Devices {
					dstPool.Devices = append(dstPool.Devices, device)
				}
				for key, val := range pPool.KeyValue {
					dstPool.KeyValue[key] = val
				}
				dstPDomain.Pools[keyPool] = dstPool
			}
			dstNode.ProvidesDomains[keyDomain] = dstPDomain
		}
		for keyDomain, cDomain := range node.ConsumesDomains {
			dstCDomain := &types.ProtectionDomain{
				Name:     cDomain.Name,
				KeyValue: make(map[string]string),
				Pools:    make(map[string]*types.StoragePool),
			}
			for key, val := range cDomain.KeyValue {
				dstCDomain.KeyValue[key] = val
			}
			for keyPool, pPool := range cDomain.Pools {
				dstPool := &types.StoragePool{
					Name:     pPool.Name,
					Devices:  make([]string, 0),
					KeyValue: make(map[string]string),
				}
				for _, device := range pPool.Devices {
					dstPool.Devices = append(dstPool.Devices, device)
				}
				for key, val := range pPool.KeyValue {
					dstPool.KeyValue[key] = val
				}
				dstCDomain.Pools[keyPool] = dstPool
			}
			dstNode.ConsumesDomains[keyDomain] = dstCDomain
		}

		dst.ScaleIO.Nodes = append(dst.ScaleIO.Nodes, dstNode)
	}

	return dst
}

//MonitorForState monitors for changes in state
func (s *RestServer) MonitorForState() error {

	var err error
	for {
		time.Sleep(time.Duration(common.PollStatusInSeconds) * time.Second)

		//must make a copy of the state because these operations can take a long time
		s.Lock()
		copyState := cloneState(s.State)
		s.Unlock()

		if common.SyncRunState(copyState, types.StateAddResourcesToScaleIO, true) {
			log.Debugln("Calling addResourcesToScaleIO()...")
			err := s.addResourcesToScaleIO(copyState)
			if err != nil {
				log.Errorln("addResourcesToScaleIO err:", err)
			}
			s.updateNodeState(types.StateInstallRexRay)
		}
		//to add more else if { SyncRunState(otherState) }
	}

	return err
}

func doesNeedleExist(haystack []string, needle string) bool {
	if haystack == nil {
		return false
	}
	for _, value := range haystack {
		log.Debugln(haystack, "=", needle, "?")
		if value == needle {
			log.Debugln(haystack, "=", needle, "? FOUND!")
			return true
		}
	}
	log.Debugln("doesNeedleExist(", needle, ") NOT FOUND!")
	return false
}

func (s *RestServer) processDeletions(metaData *kvstore.Metadata, node *types.ScaleIONode) bool {
	log.Debugln("processDeletions ENTER")

	if node == nil {
		log.Debugln("node == nil. Invalid node!")
		log.Debugln("processDeletions LEAVE")
		return false
	}
	if metaData == nil {
		log.Debugln("metaData == nil. Means no previous state! First start!")
		log.Debugln("processDeletions LEAVE")
		return false
	}

	bHasChange := false
	log.Debugln("metaData.ProtectionDomains size:", len(metaData.ProtectionDomains))
	for keyD, mDomain := range metaData.ProtectionDomains {
		log.Debugln("Domain:", mDomain.Name)
		nDomain := node.ProvidesDomains[keyD]
		if nDomain == nil {
			log.Debugln("Delete Domain:", mDomain.Name)
			mDomain.Delete = true
			bHasChange = true
			continue
		} else if len(nDomain.Pools) == 0 {
			log.Debugln("Delete Domain (", mDomain.Name, ") because contains no Pools")
			mDomain.Delete = true
			bHasChange = true
		}

		log.Debugln("domain.Sdss size:", len(mDomain.Sdss))
		for keyS, mSds := range mDomain.Sdss {
			log.Debugln("Sds:", keyS)
			if mDomain.Delete {
				log.Debugln("Delete SDS :", mSds.Name)
				mSds.Delete = true
				bHasChange = true
			}
		}

		log.Debugln("domain.Pools size:", len(mDomain.Pools))
		for keyP, mPool := range mDomain.Pools {
			log.Debugln("Pool:", keyP)
			nPool := nDomain.Pools[keyP]
			if nPool == nil {
				log.Debugln("Delete Pool:", mPool.Name)
				mPool.Delete = true
				bHasChange = true
				continue
			} else if mDomain.Delete || len(nPool.Devices) == 0 {
				log.Debugln("Delete Pool (", mPool.Name, ") because contains no Devices")
				mPool.Delete = true
				bHasChange = true
			}

			log.Debugln("pool.Devices size:", len(mPool.Devices))
			for keyDv, mDevice := range mPool.Devices {
				log.Debugln("Device:", keyDv)
				nDevices := nPool.Devices
				if nDevices == nil {
					log.Debugln("nDevices == nil")
					continue
				}
				if mDomain.Delete || mPool.Delete || !doesNeedleExist(nDevices, mDevice.Name) {
					log.Debugln("Delete Device:", mDevice.Name)
					mDevice.Delete = true
					bHasChange = true
				}
			}
		}
	}

	log.Debugln("processDeletions Succeeded. Changes:", bHasChange)
	log.Debugln("processDeletions LEAVE")

	return bHasChange
}

func (s *RestServer) processAdditions(metaData *kvstore.Metadata, node *types.ScaleIONode) bool {
	log.Debugln("processAdditions ENTER")

	if node == nil {
		log.Debugln("node == nil. Invalid node!")
		log.Debugln("processAdditions LEAVE")
		return false
	}
	if metaData == nil {
		log.Debugln("metaData == nil. Means no previous state! First start!")
		log.Debugln("processAdditions LEAVE")
		return false
	}

	bHasChange := false
	//Domain
	for keyD, nDomain := range node.ProvidesDomains {
		if metaData.ProtectionDomains == nil {
			metaData.ProtectionDomains = make(map[string]*kvstore.ProtectionDomain)
		}
		if metaData.ProtectionDomains[keyD] == nil {
			log.Debugln("Creating new ProtectionDomain:", nDomain.Name)
			metaData.ProtectionDomains[keyD] = &kvstore.ProtectionDomain{
				Name: keyD,
				Add:  true,
			}
			bHasChange = true
		} else {
			log.Debugln("ProtectionDomain (", keyD, ") already exists")
		}
		mDomain := metaData.ProtectionDomains[keyD]

		//Sds
		//TODO assume only a single Sds per ProtectionDomain. Will change later.
		//As such, the SDS is implicitly created.
		sdsName := node.IPAddress
		if mDomain.Sdss == nil {
			mDomain.Sdss = make(map[string]*kvstore.Sds)
		}
		if mDomain.Sdss[sdsName] == nil {
			mDomain.Sdss[sdsName] = &kvstore.Sds{
				Name: sdsName,
				Add:  true,
				Mode: "all",
			}
			bHasChange = true
		} else {
			log.Debugln("SDS (", sdsName, ") already exists")
		}

		//TODO: FaultSets
		/*
			for keyFS, nFaultSet := range nDomain.FaultSets {

			}
		*/

		//Pool
		for keyP, nPool := range nDomain.Pools {
			if mDomain.Pools[keyP] == nil {
				log.Debugln("Creating new StoragePool (", nPool.Name, ") for domain (", nDomain.Name, ")")
				if mDomain.Pools == nil {
					mDomain.Pools = make(map[string]*kvstore.StoragePool)
				}
				mDomain.Pools[keyP] = &kvstore.StoragePool{
					Name: keyP,
					Add:  true,
				}
				bHasChange = true
			} else {
				log.Debugln("StoragePool (", nPool.Name, ") already exists for domain (", nDomain.Name, ")")
			}
			mPool := mDomain.Pools[keyP]

			//Device
			for _, device := range nPool.Devices {
				if mPool.Devices[device] == nil {
					log.Debugln("Creating new Device (", device, ") for domain (", nDomain.Name, ") and pool (", nPool.Name, ")")
					if mPool.Devices == nil {
						mPool.Devices = make(map[string]*kvstore.Device)
					}
					mPool.Devices[device] = &kvstore.Device{
						Name: device,
						Add:  true,
					}
					bHasChange = true
				} else {
					log.Debugln("Device (", device, ") already exists for domain (", nDomain.Name, ") and pool (", nPool.Name, ")")
				}
			}
		}
	}

	log.Debugln("processAdditions Succeeded. Changes:", bHasChange)
	log.Debugln("processAdditions LEAVE")

	return bHasChange
}

func (s *RestServer) processMetadata(client *goscaleio.Client, node *types.ScaleIONode,
	metaData *kvstore.Metadata) error {
	log.Debugln("processMetadata ENTER")

	system, err := client.FindSystem(s.State.ScaleIO.ClusterID, s.State.ScaleIO.ClusterName, "")
	if err != nil {
		log.Errorln("FindSystem Error:", err)
		log.Debugln("processMetadata LEAVE")
		return err
	}

	//ProtectionDomain
	for _, domain := range metaData.ProtectionDomains {
		tmpDomain, errDomain := system.FindProtectionDomain("", domain.Name, "")
		if errDomain != nil {
			if !domain.Delete && domain.Add {
				_, err := system.CreateProtectionDomain(domain.Name)
				if err == nil {
					log.Infoln("ProtectionDomain created:", domain.Name)
				} else {
					log.Errorln("CreateProtectionDomain Error:", err)
					log.Debugln("processMetadata LEAVE")
					return err
				}
			}
			tmpDomain, errDomain = system.FindProtectionDomain("", domain.Name, "")
			if errDomain == nil {
				log.Infoln("ProtectionDomain found:", domain.Name)
			} else {
				log.Errorln("FindProtectionDomain Error:", errDomain)
				log.Debugln("processMetadata LEAVE")
				return errDomain
			}
		} else {
			log.Infoln("ProtectionDomain exists:", domain.Name)
		}
		scaleioDomain := goscaleio.NewProtectionDomainEx(client, tmpDomain)

		//Sds
		var scaleioSds *goscaleio.Sds

		for _, sds := range domain.Sdss {
			tmpSds, errSds := scaleioDomain.FindSds("Name", sds.Name)
			if errSds != nil {
				if !sds.Delete && sds.Add {
					//TODO fix the IPAddress when ServerOnly and ClientOnly is implemented
					_, err := scaleioDomain.CreateSds(sds.Name, []string{sds.Name}, []string{sds.Mode}, sds.FaultSet)
					if err == nil {
						log.Infoln("SDS created:", sds.Name)
					} else {
						log.Errorln("CreateSds Error:", err)
						log.Debugln("processMetadata LEAVE")
						return err
					}
					sds.Add = false
				}
				tmpSds, errSds = scaleioDomain.FindSds("Name", sds.Name)
				if errSds == nil {
					log.Infoln("SDS found:", sds.Name, "FaultSet:", tmpSds.FaultSetID)
				} else {
					log.Errorln("FindSds Error:", errSds)
					log.Debugln("processMetadata LEAVE")
					return errSds
				}
			} else {
				log.Infoln("SDS exists:", sds.Name, "FaultSet:", tmpSds.FaultSetID)
			}
		}

		//StoragePool
		for _, pool := range domain.Pools {
			tmpPool, errPool := scaleioDomain.FindStoragePool("", pool.Name, "")
			if errPool != nil {
				if !pool.Delete && pool.Add {
					_, err := scaleioDomain.CreateStoragePool(pool.Name)
					if err == nil {
						log.Infoln("StoragePool created:", pool.Name)
					} else {
						log.Errorln("CreateStoragePool Error:", err)
						log.Debugln("processMetadata LEAVE")
						return err
					}
					pool.Add = false
				}
				tmpPool, errPool = scaleioDomain.FindStoragePool("", pool.Name, "")
				if errPool == nil {
					log.Infoln("StoragePool found:", pool.Name)
				} else {
					log.Errorln("FindStoragePool Error:", errPool)
					log.Debugln("processMetadata LEAVE")
					return errPool
				}
			} else {
				log.Infoln("StoragePool exists:", pool.Name)
			}
			scaleioPool := goscaleio.NewStoragePoolEx(client, tmpPool)

			for _, device := range pool.Devices {
				if device.Delete {
					//TODO API Call DEL Device from Pool
				} else if device.Add {
					_, err := scaleioPool.AttachDevice(device.Name, scaleioSds.Sds.ID)
					if err == nil {
						log.Infoln("Device attached:", device.Name)
					} else {
						log.Errorln("AttachDevice Error:", err)
						log.Debugln("processMetadata LEAVE")
						return err
					}
				}
			}

			if pool.Delete {
				//TODO API Call DEL Pool
			}
		}

		if domain.Delete {
			//TODO API Call DEL SDS
			//TODO API Call DEL Domain
		}
	}

	return nil
}

func (s *RestServer) createScaleioClient(state *types.ScaleIOFramework) (*goscaleio.Client, error) {
	log.Debugln("createScaleioClient ENTER")

	ip, err := common.GetGatewayAddress(state)
	if err != nil {
		log.Errorln("GetGatewayAddress Error:", err)
		log.Debugln("createScaleioClient LEAVE")
		return nil, err
	}

	endpoint := "https://" + ip + "/api"
	log.Infoln("Endpoint:", endpoint)
	log.Infoln("APIVersion:", s.Config.APIVersion)

	client, err := goscaleio.NewClientWithArgs(endpoint, s.Config.APIVersion, true, false)
	if err != nil {
		log.Errorln("NewClientWithArgs Error:", err)
		log.Debugln("createScaleioClient LEAVE")
		return nil, err
	}

	_, err = client.Authenticate(&goscaleio.ConfigConnect{
		Endpoint: endpoint,
		Username: "admin",
		Password: s.Config.AdminPassword,
	})
	if err != nil {
		log.Errorln("Authenticate Error:", err)
		log.Debugln("addResourcesToScaleIO LEAVE")
		return nil, err
	}
	log.Infoln("Successfuly logged in to ScaleIO Gateway at", client.SIOEndpoint.String())

	log.Debugln("createScaleioClient Succeeded")
	log.Debugln("createScaleioClient LEAVE")

	return client, nil
}

func (s *RestServer) addResourcesToScaleIO(state *types.ScaleIOFramework) error {
	log.Debugln("addResourcesToScaleIO ENTER")

	var client *goscaleio.Client
	client = nil

	for _, node := range state.ScaleIO.Nodes {
		log.Debugln("Processing node:", node.Hostname)

		if !node.Declarative && !node.Advertised {
			log.Warnln("This node has not advertised its devices yet. Skip!")
			continue
		}

		metaData, err := s.Store.GetMetadata(node.Hostname)
		if err != nil {
			log.Warnln("No metadata for node", node.Hostname)
		}

		if metaData == nil {
			log.Debugln("Creating new metadata object. No prior state.")
			metaData = new(kvstore.Metadata)
		}

		//look for deletions
		dChanges := s.processDeletions(metaData, node)

		//look for additions
		aChanges := s.processAdditions(metaData, node)

		if !dChanges && !aChanges {
			log.Debugln("There are no new changes for this node.")
			continue
		}

		if client == nil {
			client, err = s.createScaleioClient(state)
			if err != nil {
				log.Errorln("createScaleioClient Failed. Err:", err)
				log.Debugln("addResourcesToScaleIO LEAVE")
				return err
			}
		}

		//process metadata model
		s.processMetadata(client, node, metaData)

		err = s.Store.SetMetadata(node.Hostname, metaData)
		if err == nil {
			log.Debugln("Metadata saved for node:", node.Hostname)
		} else {
			log.Errorln("Save metadata failed for node: ", node.Hostname, ". Err:", err)
		}
	}

	log.Debugln("addResourcesToScaleIO Succeeded!")
	log.Debugln("addResourcesToScaleIO LEAVE")

	return nil
}

func (s *RestServer) updateNodeState(state int) {
	s.Lock()

	for i := 0; i < len(s.State.ScaleIO.Nodes); i++ {
		if s.State.ScaleIO.Nodes[i].State > state { //only update state if less than current
			continue
		}
		s.State.ScaleIO.Nodes[i].State = state
	}

	s.Unlock()
}
