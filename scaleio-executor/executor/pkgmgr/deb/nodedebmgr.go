package deb

import (
	log "github.com/Sirupsen/logrus"
	xplatform "github.com/dvonthenen/goxplatform"

	mgr "github.com/codedellemc/scaleio-framework/scaleio-executor/executor/pkgmgr/mgr"
	types "github.com/codedellemc/scaleio-framework/scaleio-scheduler/types"
)

const (
	//Environment
	aiozipCheck                = "[0-9]+ upgraded|[0-9]+ newly"
	genericInstallCheck        = "1 upgraded|1 newly"
	requiredKernelVersionCheck = "4.2.0-30-generic"

	//ScaleIO node
	sdsInstallCheck = "sds start/running"
	sdcInstallCheck = "Success configuring module"

	//REX-Ray
	rexrayInstallCheck = "rexray has been installed to"

	//Isolator
	dvdcliInstallCheck = "dvdcli has been installed to"
)

//NodeDebMgr implementation for NodeDebMgr
type NodeDebMgr struct {
	*mgr.NodeManager
}

//EnvironmentSetup for setting up the environment for ScaleIO
func (dpm *NodeDebMgr) EnvironmentSetup(state *types.ScaleIOFramework) (bool, error) {
	log.Infoln("EnvironmentSetup ENTER")

	aioErr := xplatform.GetInstance().Inst.IsInstalled("libaio1")
	zipErr := xplatform.GetInstance().Inst.IsInstalled("zip")
	if aioErr != nil || zipErr != nil {
		log.Infoln("Installing libaio1 and zip")

		miscCmdline := "apt-get -y install libaio1 zip"
		err := xplatform.GetInstance().Run.Command(miscCmdline, aiozipCheck, "")
		if err != nil {
			log.Errorln("Install Prerequisites Failed:", err)
			log.Infoln("EnvironmentSetup LEAVE")
			return false, err
		}
	} else {
		log.Infoln("libaio1 and zip are already installed")
	}

	kernelErr := xplatform.GetInstance().Inst.IsInstalled("linux-image-4.2.0-30-generic")
	if kernelErr != nil {
		log.Infoln("Installing linux-image-4.2.0-30-generic")

		kernelCmdline := "apt-get -y install linux-image-4.2.0-30-generic"
		err := xplatform.GetInstance().Run.Command(kernelCmdline, genericInstallCheck, "")
		if err != nil {
			log.Errorln("Install Kernel Failed:", err)
			log.Infoln("EnvironmentSetup LEAVE")
			return false, err
		}
	} else {
		log.Infoln("linux-image-4.2.0-30-generic is already installed")
	}

	//get running kernel version
	kernelVer, kernelVerErr := xplatform.GetInstance().Sys.GetRunningKernelVersion()
	if kernelVerErr != nil {
		log.Errorln("Kernel Version Check Failed:", kernelVerErr)
		log.Infoln("EnvironmentSetup LEAVE")
		return false, kernelVerErr
	}

	if kernelVer != requiredKernelVersionCheck {
		log.Errorln("Kernel is installed but not running. Reboot Required!")
		log.Infoln("EnvironmentSetup LEAVE")
		return true, nil
	}
	log.Infoln("Already running kernel version", requiredKernelVersionCheck)
	//get running kernel version

	log.Infoln("EnvironmentSetup Succeeded")
	log.Infoln("EnvironmentSetup LEAVE")
	return false, nil
}

//NewNodeDebMgr generates a NodeDebMgr object
func NewNodeDebMgr(state *types.ScaleIOFramework) NodeDebMgr {
	myNodeMgr := &mgr.NodeManager{}
	myNodeDebMgr := myNodeDebMgr{myNodeMgr}

	//ScaleIO node
	myNodeDebMgr.BaseManager.SdsPackageName = types.DebSdsPackageName
	myNodeDebMgr.BaseManager.SdsPackageDownload = state.ScaleIO.Deb.DebSds
	myNodeDebMgr.BaseManager.SdsInstallCmd = "dpkg -i {LocalSds}"
	myNodeDebMgr.BaseManager.SdsInstallCheck = sdsInstallCheck
	myNodeDebMgr.BaseManager.SdcPackageName = types.DebSdcPackageName
	myNodeDebMgr.BaseManager.SdcPackageDownload = state.ScaleIO.Deb.DebSdc
	myNodeDebMgr.BaseManager.SdcInstallCmd = "MDM_IP={MdmPair} dpkg -i {LocalSdc}"
	myNodeDebMgr.BaseManager.SdcInstallCheck = sdcInstallCheck

	//REX-Ray
	myNodeDebMgr.BaseManager.RexrayInstallCheck = rexrayInstallCheck

	//Isolator
	myNodeDebMgr.BaseManager.DvdcliInstallCheck = dvdcliInstallCheck

	return myNodeDebMgr
}
