/*
Copyright 2024 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hygon

import (
	"errors"
	"flag"
	"slices"
	"strings"

	"github.com/Project-HAMi/HAMi/pkg/util"
	"github.com/Project-HAMi/HAMi/pkg/util/nodelock"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

type DCUDevices struct {
}

const (
	HandshakeAnnos     = "hami.io/node-handshake-dcu"
	RegisterAnnos      = "hami.io/node-dcu-register"
	HygonDCUDevice     = "DCU"
	HygonDCUCommonWord = "DCU"
	DCUInUse           = "hygon.com/use-dcutype"
	DCUNoUse           = "hygon.com/nouse-dcutype"
	// DCUUseUUID is user can use specify DCU device for set DCU UUID.
	DCUUseUUID = "hygon.com/use-gpuuuid"
	// DCUNoUseUUID is user can not use specify DCU device for set DCU UUID.
	DCUNoUseUUID = "hygon.com/nouse-gpuuuid"

	// NodeLockDCU should same with device plugin node lock name
	// there is a bug with nodelock package utils, the key is hard coded as "hami.io/mutex.lock"
	// so we can only use this value now.
	NodeLockDCU = "hami.io/mutex.lock"
)

var (
	HygonResourceCount  string
	HygonResourceMemory string
	HygonResourceCores  string
)

type HygonConfig struct {
	ResourceCountName  string `yaml:"resourceCountName"`
	ResourceMemoryName string `yaml:"resourceMemoryName"`
	ResourceCoreName   string `yaml:"resourceCoreName"`
}

func InitDCUDevice(config HygonConfig) *DCUDevices {
	HygonResourceCount = config.ResourceCountName
	HygonResourceMemory = config.ResourceMemoryName
	HygonResourceCores = config.ResourceCoreName
	util.InRequestDevices[HygonDCUDevice] = "hami.io/dcu-devices-to-allocate"
	util.SupportDevices[HygonDCUDevice] = "hami.io/dcu-devices-allocated"
	util.HandshakeAnnos[HygonDCUDevice] = HandshakeAnnos
	return &DCUDevices{}
}

func (dev *DCUDevices) CommonWord() string {
	return HygonDCUCommonWord
}

func ParseConfig(fs *flag.FlagSet) {
	fs.StringVar(&HygonResourceCount, "dcu-name", "hygon.com/dcunum", "dcu resource count")
	fs.StringVar(&HygonResourceMemory, "dcu-memory", "hygon.com/dcumem", "dcu memory resource")
	fs.StringVar(&HygonResourceCores, "dcu-cores", "hygon.com/dcucores", "dcu core resource")
}

func (dev *DCUDevices) MutateAdmission(ctr *corev1.Container, p *corev1.Pod) (bool, error) {
	_, ok := ctr.Resources.Limits[corev1.ResourceName(HygonResourceCount)]
	return ok, nil
}

func checkDCUtype(annos map[string]string, cardtype string) bool {
	if inuse, ok := annos[DCUInUse]; ok {
		if !strings.Contains(inuse, ",") {
			if strings.Contains(strings.ToUpper(cardtype), strings.ToUpper(inuse)) {
				return true
			}
		} else {
			for _, val := range strings.Split(inuse, ",") {
				if strings.Contains(strings.ToUpper(cardtype), strings.ToUpper(val)) {
					return true
				}
			}
		}
		return false
	}
	if nouse, ok := annos[DCUNoUse]; ok {
		if !strings.Contains(nouse, ",") {
			if strings.Contains(strings.ToUpper(cardtype), strings.ToUpper(nouse)) {
				return false
			}
		} else {
			for _, val := range strings.Split(nouse, ",") {
				if strings.Contains(strings.ToUpper(cardtype), strings.ToUpper(val)) {
					return false
				}
			}
		}
		return true
	}
	return true
}

func (dev *DCUDevices) LockNode(n *corev1.Node, p *corev1.Pod) error {
	found := false
	for _, val := range p.Spec.Containers {
		if (dev.GenerateResourceRequests(&val).Nums) > 0 {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return nodelock.LockNode(n.Name, NodeLockDCU, p)
}

func (dev *DCUDevices) ReleaseNodeLock(n *corev1.Node, p *corev1.Pod) error {
	found := false
	for _, val := range p.Spec.Containers {
		if (dev.GenerateResourceRequests(&val).Nums) > 0 {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return nodelock.ReleaseNodeLock(n.Name, NodeLockDCU, p, false)
}

func (dev *DCUDevices) GetNodeDevices(n corev1.Node) ([]*util.DeviceInfo, error) {
	devEncoded, ok := n.Annotations[RegisterAnnos]
	if !ok {
		return []*util.DeviceInfo{}, errors.New("annos not found " + RegisterAnnos)
	}
	nodedevices, err := util.DecodeNodeDevices(devEncoded)
	if err != nil {
		klog.ErrorS(err, "failed to decode node devices", "node", n.Name, "device annotation", devEncoded)
		return []*util.DeviceInfo{}, err
	}
	if len(nodedevices) == 0 {
		klog.InfoS("no gpu device found", "node", n.Name, "device annotation", devEncoded)
		return []*util.DeviceInfo{}, errors.New("no gpu found on node")
	}
	devDecoded := util.EncodeNodeDevices(nodedevices)
	klog.V(5).InfoS("nodes device information", "node", n.Name, "nodedevices", devDecoded)
	return nodedevices, nil
}

func (dev *DCUDevices) NodeCleanUp(nn string) error {
	return util.MarkAnnotationsToDelete(HandshakeAnnos, nn)
}

func (dev *DCUDevices) CheckHealth(devType string, n *corev1.Node) (bool, bool) {
	return util.CheckHealth(devType, n)
}

func (dev *DCUDevices) CheckType(annos map[string]string, d util.DeviceUsage, n util.ContainerDeviceRequest) (bool, bool, bool) {
	if strings.Compare(n.Type, HygonDCUDevice) == 0 {
		return true, checkDCUtype(annos, d.Type), false
	}
	return false, false, false
}

func (dev *DCUDevices) CheckUUID(annos map[string]string, d util.DeviceUsage) bool {
	userUUID, ok := annos[DCUUseUUID]
	if ok {
		klog.V(5).Infof("check uuid for dcu user uuid [%s], device id is %s", userUUID, d.ID)
		// use , symbol to connect multiple uuid
		userUUIDs := strings.Split(userUUID, ",")
		return slices.Contains(userUUIDs, d.ID)
	}

	noUserUUID, ok := annos[DCUNoUseUUID]
	if ok {
		klog.V(5).Infof("check uuid for dcu not user uuid [%s], device id is %s", noUserUUID, d.ID)
		// use , symbol to connect multiple uuid
		noUserUUIDs := strings.Split(noUserUUID, ",")
		return !slices.Contains(noUserUUIDs, d.ID)
	}
	return true
}

func (dev *DCUDevices) GenerateResourceRequests(ctr *corev1.Container) util.ContainerDeviceRequest {
	klog.Info("Start to count dcu devices for container ", ctr.Name)
	dcuResourceCount := corev1.ResourceName(HygonResourceCount)
	dcuResourceMem := corev1.ResourceName(HygonResourceMemory)
	dcuResourceCores := corev1.ResourceName(HygonResourceCores)
	v, ok := ctr.Resources.Limits[dcuResourceCount]
	if !ok {
		v, ok = ctr.Resources.Requests[dcuResourceCount]
	}
	if ok {
		if n, ok := v.AsInt64(); ok {
			klog.Info("Found dcu devices")
			memnum := 0
			mem, ok := ctr.Resources.Limits[dcuResourceMem]
			if !ok {
				mem, ok = ctr.Resources.Requests[dcuResourceMem]
			}
			if ok {
				memnums, ok := mem.AsInt64()
				if ok {
					memnum = int(memnums)
				}
			}
			corenum := int32(100)
			core, ok := ctr.Resources.Limits[dcuResourceCores]
			if !ok {
				core, ok = ctr.Resources.Requests[dcuResourceCores]
			}
			if ok {
				corenums, ok := core.AsInt64()
				if ok {
					corenum = int32(corenums)
				}
			}

			mempnum := 0
			if memnum == 0 {
				mempnum = 100
			}

			return util.ContainerDeviceRequest{
				Nums:             int32(n),
				Type:             HygonDCUDevice,
				Memreq:           int32(memnum),
				MemPercentagereq: int32(mempnum),
				Coresreq:         corenum,
			}
		}
	}
	return util.ContainerDeviceRequest{}
}

func (dev *DCUDevices) PatchAnnotations(annoinput *map[string]string, pd util.PodDevices) map[string]string {
	devlist, ok := pd[HygonDCUDevice]
	if ok && len(devlist) > 0 {
		deviceStr := util.EncodePodSingleDevice(devlist)
		(*annoinput)[util.InRequestDevices[HygonDCUDevice]] = deviceStr
		(*annoinput)[util.SupportDevices[HygonDCUDevice]] = deviceStr
		klog.V(5).Infof("pod add notation key [%s], values is [%s]", util.InRequestDevices[HygonDCUDevice], deviceStr)
		klog.V(5).Infof("pod add notation key [%s], values is [%s]", util.SupportDevices[HygonDCUDevice], deviceStr)
	}
	return *annoinput
}

func (dev *DCUDevices) CustomFilterRule(allocated *util.PodDevices, request util.ContainerDeviceRequest, toAllocate util.ContainerDevices, device *util.DeviceUsage) bool {
	return true
}

func (dev *DCUDevices) ScoreNode(node *corev1.Node, podDevices util.PodSingleDevice, policy string) float32 {
	return 0
}

func (dev *DCUDevices) AddResourceUsage(n *util.DeviceUsage, ctr *util.ContainerDevice) error {
	n.Used++
	n.Usedcores += ctr.Usedcores
	n.Usedmem += ctr.Usedmem
	return nil
}
