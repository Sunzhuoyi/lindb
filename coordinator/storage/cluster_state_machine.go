// Licensed to LinDB under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. LinDB licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/lindb/lindb/config"
	"github.com/lindb/lindb/constants"
	"github.com/lindb/lindb/coordinator/discovery"
	"github.com/lindb/lindb/coordinator/inif"
	"github.com/lindb/lindb/coordinator/task"
	"github.com/lindb/lindb/pkg/encoding"
	"github.com/lindb/lindb/pkg/logger"
	"github.com/lindb/lindb/pkg/ltoml"
	"github.com/lindb/lindb/pkg/state"
)

//go:generate mockgen -source=./cluster_state_machine.go -destination=./cluster_state_machine_mock.go -package=storage

// ClusterStateMachine represents storage cluster control when node is master,
// watches cluster config change event, then create/delete related storage cluster controller.
type ClusterStateMachine interface {
	inif.Listener
	io.Closer

	// GetCluster returns cluster controller for maintain the metadata of storage cluster
	GetCluster(name string) Cluster
	// GetAllCluster returns all cluster controller
	GetAllCluster() []Cluster
}

// clusterStateMachine implements storage cluster state machine,
// maintain cluster controller for controlling cluster's metadata
type clusterStateMachine struct {
	repo      state.Repository
	discovery discovery.Discovery
	ctx       context.Context
	cancel    context.CancelFunc

	clusterFactory    ClusterFactory
	discoveryFactory  discovery.Factory
	repoFactory       state.RepositoryFactory
	controllerFactory task.ControllerFactory

	clusters map[string]Cluster

	interval time.Duration
	timer    *time.Timer

	running *atomic.Bool
	mutex   sync.RWMutex
	logger  *logger.Logger
}

// NewClusterStateMachine create state machine, init cluster controller if exist, watch change event
func NewClusterStateMachine(
	ctx context.Context,
	repo state.Repository,
	controllerFactory task.ControllerFactory,
	discoveryFactory discovery.Factory,
	clusterFactory ClusterFactory,
	repoFactory state.RepositoryFactory,
) (ClusterStateMachine, error) {
	log := logger.GetLogger("coordinator", "StorageClusterStateMachine")
	c, cancel := context.WithCancel(ctx)
	stateMachine := &clusterStateMachine{
		repo:              repo,
		ctx:               c,
		cancel:            cancel,
		clusterFactory:    clusterFactory,
		discoveryFactory:  discoveryFactory,
		repoFactory:       repoFactory,
		controllerFactory: controllerFactory,
		clusters:          make(map[string]Cluster),
		running:           atomic.NewBool(false),
		interval:          30 * time.Second, //TODO add config ?
		logger:            log,
	}

	// new storage config discovery
	stateMachine.discovery = discoveryFactory.CreateDiscovery(constants.StorageClusterConfigPath, stateMachine)
	if err := stateMachine.discovery.Discovery(true); err != nil {
		return nil, fmt.Errorf("discovery storage cluster config error:%s", err)
	}
	// start collect cluster stat goroutine
	stateMachine.timer = time.NewTimer(stateMachine.interval)
	go stateMachine.collectStat()

	stateMachine.running.Store(true)
	log.Info("storage cluster state machine started")
	return stateMachine, nil
}

// OnCreate creates and starts cluster controller when receive create event
func (c *clusterStateMachine) OnCreate(key string, resource []byte) {
	c.logger.Info("storage cluster be created", logger.String("key", key))
	c.addCluster(resource)
}

// OnDelete deletes cluster controller from cache, closes it
func (c *clusterStateMachine) OnDelete(key string) {
	_, name := filepath.Split(key)
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.deleteCluster(name)
}

// GetCluster returns cluster controller for maintain the metadata of storage cluster
func (c *clusterStateMachine) GetCluster(name string) Cluster {
	var cluster Cluster
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if !c.running.Load() {
		return cluster
	}

	cluster = c.clusters[name]
	return cluster
}

// GetAllCluster returns all cluster controller
func (c *clusterStateMachine) GetAllCluster() []Cluster {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if !c.running.Load() {
		return nil
	}

	var clusters []Cluster
	for _, v := range c.clusters {
		clusters = append(clusters, v)
	}
	return clusters
}

// Close closes state machine, cleanup and close all cluster controller
func (c *clusterStateMachine) Close() error {
	if c.running.CAS(true, false) {
		c.mutex.Lock()
		defer func() {
			c.mutex.Unlock()
			c.timer.Stop()
			c.cancel()
		}()
		// 1) close listen for storage cluster config change
		c.discovery.Close()
		// 2) cleanup clusters and release resource
		c.cleanupCluster()
	}
	return nil
}

func (c *clusterStateMachine) collectStat() {
	for {
		select {
		case <-c.timer.C:
			c.collect()
			// reset time interval
			c.timer.Reset(c.interval)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *clusterStateMachine) collect() {
	c.logger.Debug("collecting storage cluster stat")

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	for name, cluster := range c.clusters {
		stat, err := cluster.CollectStat()
		if err != nil {
			c.logger.Warn("collect storage cluster stat", logger.String("cluster", name), logger.Error(err))
			continue
		}
		stat.Name = name
		if err := c.repo.Put(c.ctx, constants.GetStorageClusterStatPath(name), encoding.JSONMarshal(stat)); err != nil {
			c.logger.Warn("save storage cluster stat", logger.String("cluster", name), logger.Error(err))
			continue
		}
	}
}

// cleanupCluster cleanups cluster controller
func (c *clusterStateMachine) cleanupCluster() {
	for _, v := range c.clusters {
		v.Close()
	}
}

// addCluster creates and starts cluster controller, if success cache it
func (c *clusterStateMachine) addCluster(resource []byte) {
	cfg := config.StorageCluster{}
	if err := encoding.JSONUnmarshal(resource, &cfg); err != nil {
		c.logger.Error("discovery new storage config but unmarshal error",
			logger.String("data", string(resource)), logger.Error(err))
		return
	}
	if len(cfg.Name) == 0 {
		c.logger.Error("cluster name is empty", logger.Any("cfg", cfg))
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// shutdown old cluster state machine if exist
	c.deleteCluster(cfg.Name)

	//TODO need add config, and retry???
	cfg.Config.Timeout = ltoml.Duration(10 * time.Second)
	cfg.Config.DialTimeout = ltoml.Duration(5 * time.Second)

	storageRepo, err := c.repoFactory.CreateRepo(cfg.Config)
	if err != nil {
		c.logger.Error("new state repo error when create cluster",
			logger.Any("cfg", cfg), logger.Error(err))
		return
	}
	clusterCfg := clusterCfg{
		ctx:               c.ctx,
		cfg:               cfg,
		brokerRepo:        c.repo,
		storageRepo:       storageRepo,
		controllerFactory: c.controllerFactory,
		factory:           discovery.NewFactory(storageRepo),
		logger:            c.logger,
	}
	cluster, err := c.clusterFactory.newCluster(clusterCfg)
	if err != nil {
		// IMPORTANT!!!!!!!: need clean cluster cfg resource when new cluster fail
		if cluster != nil {
			cluster.Close()
		}
		(&clusterCfg).clean()
		c.logger.Error("create storage cluster error",
			logger.Any("cfg", cfg), logger.Error(err))
		return
	}
	c.clusters[cfg.Name] = cluster
}

// deleteCluster deletes the cluster if exist
func (c *clusterStateMachine) deleteCluster(name string) {
	cluster, ok := c.clusters[name]
	if ok {
		// need cleanup cluster resource
		cluster.Close()
		delete(c.clusters, name)
	}
}
