======
Repair
======

.. toctree::
   :hidden:
   :maxdepth: 2

   repair-faster
   repair-slower
   examples

.. contents::
   :depth: 2
   :local:

Repair is important to make sure that data across the nodes is consistent.
To learn more about repairs please consult `this Scylla University lesson <https://university.scylladb.com/courses/scylla-operations/lessons/scylla-manager-repair-and-tombstones/topic/repairs>`_.

Scylla Manager automates the repair process and allows you to configure how and when repair occurs.
*Scylla Manager repair task* revolves around scheduling many *Scylla repair jobs* with selected ``--intensity`` in ``--parallel``.
*Repair task* is responsible for fully repairing all tables selected with ``--keyspace`` parameter, while a single *repair job* repairs
chosen (by Scylla Manager) token ranges of a given table owned by a specific replica set. All nodes from this replica set take part in
the repair job and any node can take part only in a single repair job at any given time.

When you create a cluster a repair task is automatically scheduled.
This task is set to occur each week by default, but you can change it to another time, change its parameters or add additional repair tasks if needed.

Features
========

* Glob patterns to select keyspaces or tables to repair
* Parallel repairs
* Control over repair intensity and parallelism even for ongoing repairs
* Repair order improving performance and stability
* Resilience to schema changes
* Retries
* Pause and resume

Parallel repairs
================

Each node can take part in at most one Scylla repair job at any given moment, but Scylla Manager can repair distinct replica sets in a token ring in parallel.
This is beneficial for big clusters.
For example, a 9 node cluster and a keyspace with replication factor 3, can be repaired up to 3 times faster in parallel.
The following diagram presents a benchmark results comparing different parallel flag values.
In a benchmark we ran 9 Scylla 2020.1 nodes on AWS i3.2xlarge machines under 50% load, for details check `this blog post <https://www.scylladb.com/2020/11/12/scylla-manager-2-2-repair-revisited/>`_

.. image:: images/parallel.png
  :width: 80%

By default Scylla Manager runs repairs with full parallelism, you can change that using :ref:`sctool repair --parallel flag <sctool-repair>`.

Repair intensity
================

Intensity specifies how many token ranges can be repaired in a Scylla node at every given time.
The default intensity is one, you can change that using :ref:`sctool repair --intensity flag <sctool-repair>`.

Scylla Manager 2.2 adds support for intensity value zero.
In that case the number of token ranges is calculated based on node memory and adjusted to the Scylla maximal number of ranges that can be repaired in parallel (see ``max_repair_ranges_in_parallel`` in Scylla logs).
If you want to repair faster, try using intensity zero.

Note that the less the cluster is loaded the more it makes sense to increase intensity.
If you increase intensity on a loaded cluster it may not give speed benefits since cluster have no resources to process more repairs.
In our experiments in a 50% loaded cluster increasing intensity from 1 to 2 gives about 10-20% boost and increasing it further will have little impact.

Changing repair speed
=====================

Repair speed is controlled by two parameters: ``--parallel`` and ``--intensity``
Those parameters can be set when you:

* Schedule a repair with :ref:`sctool repair <sctool-repair>`
* Update a repair specification with :ref:`sctool repair update <reschedule-a-repair>`
* Update a running repair task with :ref:`sctool repair control <repair-control>`

More on the topic of repair speed can be found in :doc:`Repair faster <repair-faster>` and :doc:`Repair slower <repair-slower>` articles.

Repair order
============

Scylla Manager repairs keyspace by keyspace and table by table in order to achieve greater repair stability and performance.

Keyspaces and tables are ordered according to the following rules:

* repair internal (with ``system`` prefix) tables before user tables
* repair base tables before `Materialized Views <https://opensource.docs.scylladb.com/stable/using-scylla/materialized-views.html>`_ and `Secondary Indexes <https://opensource.docs.scylladb.com/stable/using-scylla/secondary-indexes.html>`_
* repair smaller keyspaces and tables first

.. note:: Ensuring that base tables are repaired before views is possible only when Scylla Manager has `CQL credentials <https://manager.docs.scylladb.com/stable/sctool/cluster.html#cluster-add>`_ to repaired cluster.
