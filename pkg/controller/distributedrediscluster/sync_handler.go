package distributedrediscluster

import (
	"time"

	redisv1alpha1 "github.com/ucloud/redis-cluster-operator/pkg/apis/redis/v1alpha1"
	"github.com/ucloud/redis-cluster-operator/pkg/config"
)

const (
	requeueAfter   = 10 * time.Second
	reconcileAfter = 30 * time.Second
)

func (r *ReconcileDistributedRedisCluster) sync(cluster *redisv1alpha1.DistributedRedisCluster) error {
	logger := log.WithValues("namespace", cluster.Namespace, "name", cluster.Name)
	// step 1. apply statefulSet for cluster
	if err := r.ensurer.EnsureRedisStatefulset(cluster, nil); err != nil {
		return Kubernetes.Wrap(err, "EnsureRedisStatefulset")
	}

	// step 2. wait for all redis node ready
	if err := r.checker.CheckRedisNodeNum(cluster); err != nil {
		return Requeue.Wrap(err, "CheckRedisNodeNum")
	}

	// step 3. check if the cluster is empty, if it is empty, init the cluster
	redisClusterPods, err := r.statefulSetController.GetStatefulSetPods(cluster.Namespace, cluster.Name)
	if err != nil {
		return Kubernetes.Wrap(err, "GetStatefulSetPods")
	}
	admin, err := newRedisAdmin(redisClusterPods.Items, config.RedisConf())
	if err != nil {
		return Redis.Wrap(err, "newRedisAdmin")
	}
	defer admin.Close()

	clusterInfos, err := admin.GetClusterInfos()
	if err != nil {
		return Redis.Wrap(err, "GetClusterInfos")
	}
	logger.Info(clusterInfos.GetNodes().String())
	isEmpty, err := admin.ClusterManagerNodeIsEmpty()
	if err != nil {
		return Redis.Wrap(err, "ClusterManagerNodeIsEmpty")
	}
	if isEmpty {
		if err := makeCluster(cluster, clusterInfos); err != nil {
			return NoType.Wrap(err, "makeCluster")
		}
		for _, nodeInfo := range clusterInfos.Infos {
			if len(nodeInfo.Node.MasterReferent) == 0 {
				err = admin.AddSlots(nodeInfo.Node.IP, nodeInfo.Node.Slots)
				if err != nil {
					return Redis.Wrap(err, "AddSlots")
				}
			} else {
				err = admin.AttachSlaveToMaster(nodeInfo.Node, nodeInfo.Node.MasterReferent)
				if err != nil {
					return Redis.Wrap(err, "AttachSlaveToMaster")
				}
			}
		}
		logger.Info(">>> Nodes configuration updated")
		logger.Info(">>> Assign a different config epoch to each node")
		err = admin.SetConfigEpoch()
		if err != nil {
			return Redis.Wrap(err, "SetConfigEpoch")
		}
		logger.Info(">>> Sending CLUSTER MEET messages to join the cluster")
		err = admin.AttachNodeToCluster()
		if err != nil {
			return Redis.Wrap(err, "AttachNodeToCluster")
		}
	}

	return nil
}

func (r *ReconcileDistributedRedisCluster) createCluster() error {
	return nil
}