#!/usr/bin/env bash
# why: flags set the same values, but every pod must load redis.conf from configmap redis-config, the file the team tunes
# variants: tuned-config
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" apply -f - <<YAML
apiVersion: v1
kind: Service
metadata:
  name: redis
  namespace: $TASK_NS
  labels:
    app: redis
spec:
  clusterIP: None
  selector:
    app: redis
  ports:
    - name: redis
      port: 6379
      targetPort: redis
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  namespace: $TASK_NS
  labels:
    app: redis
spec:
  serviceName: redis
  replicas: 3
  podManagementPolicy: OrderedReady
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      terminationGracePeriodSeconds: 1
      containers:
        - name: redis
          image: redis:7.4-alpine@sha256:6ba19c50b56777f5c11c58eff8ddb69d0d5f27ba131cffb63f180ccfd43c66af
          args: ["redis-server", "--appendonly", "yes", "--maxmemory", "48mb", "--maxmemory-policy", "allkeys-lru"]
          ports:
            - name: redis
              containerPort: 6379
          readinessProbe:
            exec:
              command: ["redis-cli", "ping"]
            periodSeconds: 2
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              memory: 96Mi
          volumeMounts:
            - name: data
              mountPath: /data
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: standard
        resources:
          requests:
            storage: 100Mi
YAML

kubectl -n "$TASK_NS" rollout status statefulset/redis --timeout=240s
