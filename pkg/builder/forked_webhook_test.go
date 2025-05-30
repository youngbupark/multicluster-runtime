/*
Copyright 2019 The Kubernetes Authors.

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

package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega/gbytes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	admissionReviewGV = `{
  "kind":"AdmissionReview",
  "apiVersion":"admission.k8s.io/`

	svcBaseAddr = "http://svc-name.svc-ns.svc"
)

var _ = Describe("webhook", func() {
	Describe("New", func() {
		Context("v1 AdmissionReview", func() {
			runTests("v1")
		})
		Context("v1beta1 AdmissionReview", func() {
			runTests("v1beta1")
		})
	})
})

func runTests(admissionReviewVersion string) {
	var (
		stop          chan struct{}
		logBuffer     *gbytes.Buffer
		testingLogger logr.Logger
	)

	BeforeEach(func() {
		stop = make(chan struct{})
		logBuffer = gbytes.NewBuffer()
		testingLogger = zap.New(zap.JSONEncoder(), zap.WriteTo(io.MultiWriter(logBuffer, GinkgoWriter)))
	})

	AfterEach(func() {
		close(stop)
	})

	It("should scaffold a custom defaulting webhook", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testDefaulterGVK.GroupVersion()}
		builder.Register(&TestDefaulter{}, &TestDefaulterList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestDefaulter{}).
			WithDefaulter(&TestCustomDefaulter{}).
			WithLogConstructor(func(base logr.Logger, req *admission.Request) logr.Logger {
				return admission.DefaultLogConstructor(testingLogger, req)
			}).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"foo.test.org",
      "version":"v1",
      "kind":"TestDefaulter"
    },
    "resource":{
      "group":"foo.test.org",
      "version":"v1",
      "resource":"testdefaulter"
    },
    "namespace":"default",
    "name":"foo",
    "operation":"CREATE",
    "object":{
      "replica":1
    },
    "oldObject":null
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a mutating webhook path")
		path := generateMutatePath(testDefaulterGVK)
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable fields")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":true`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"patch":`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":200`))
		EventuallyWithOffset(1, logBuffer).Should(gbytes.Say(`"msg":"Defaulting object","object":{"name":"foo","namespace":"default"},"namespace":"default","name":"foo","resource":{"group":"foo.test.org","version":"v1","resource":"testdefaulter"},"user":"","requestID":"07e52e8d-4513-11e9-a716-42010a800270"`))

		By("sending a request to a validating webhook path that doesn't exist")
		path = generateValidatePath(testDefaulterGVK)
		_, err = reader.Seek(0, 0)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req = httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w = httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusNotFound))
	})

	It("should scaffold a custom defaulting webhook with a custom path", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testDefaulterGVK.GroupVersion()}
		builder.Register(&TestDefaulter{}, &TestDefaulterList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		customPath := "/custom-defaulting-path"
		err = WebhookManagedBy(m).
			For(&TestDefaulter{}).
			WithDefaulter(&TestCustomDefaulter{}).
			WithLogConstructor(func(base logr.Logger, req *admission.Request) logr.Logger {
				return admission.DefaultLogConstructor(testingLogger, req)
			}).
			WithCustomPath(customPath).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"foo.test.org",
      "version":"v1",
      "kind":"TestDefaulter"
    },
    "resource":{
      "group":"foo.test.org",
      "version":"v1",
      "resource":"testdefaulter"
    },
    "namespace":"default",
    "name":"foo",
    "operation":"CREATE",
    "object":{
      "replica":1
    },
    "oldObject":null
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a mutating webhook path")
		path, err := generateCustomPath(customPath)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable fields")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":true`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"patch":`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":200`))
		EventuallyWithOffset(1, logBuffer).Should(gbytes.Say(`"msg":"Defaulting object","object":{"name":"foo","namespace":"default"},"namespace":"default","name":"foo","resource":{"group":"foo.test.org","version":"v1","resource":"testdefaulter"},"user":"","requestID":"07e52e8d-4513-11e9-a716-42010a800270"`))

		By("sending a request to a mutating webhook path that have been overrided by the custom path")
		path = generateMutatePath(testDefaulterGVK)
		_, err = reader.Seek(0, 0)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req = httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w = httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusNotFound))
	})

	It("should scaffold a custom defaulting webhook which recovers from panics", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testDefaulterGVK.GroupVersion()}
		builder.Register(&TestDefaulter{}, &TestDefaulterList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestDefaulter{}).
			WithDefaulter(&TestCustomDefaulter{}).
			RecoverPanic(true).
			// RecoverPanic defaults to true.
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"",
      "version":"v1",
      "kind":"TestDefaulter"
    },
    "resource":{
      "group":"",
      "version":"v1",
      "resource":"testdefaulter"
    },
    "namespace":"default",
    "operation":"CREATE",
    "object":{
      "replica":1,
      "panic":true
    },
    "oldObject":null
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a mutating webhook path")
		path := generateMutatePath(testDefaulterGVK)
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable fields")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":false`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":500`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"message":"panic: fake panic test [recovered]`))
	})

	It("should scaffold a custom validating webhook", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testValidatorGVK.GroupVersion()}
		builder.Register(&TestValidator{}, &TestValidatorList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestValidator{}).
			WithValidator(&TestCustomValidator{}).
			WithLogConstructor(func(base logr.Logger, req *admission.Request) logr.Logger {
				return admission.DefaultLogConstructor(testingLogger, req)
			}).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"foo.test.org",
      "version":"v1",
      "kind":"TestValidator"
    },
    "resource":{
      "group":"foo.test.org",
      "version":"v1",
      "resource":"testvalidator"
    },
    "namespace":"default",
    "name":"foo",
    "operation":"UPDATE",
    "object":{
      "replica":1
    },
    "oldObject":{
      "replica":2
    }
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a mutating webhook path that doesn't exist")
		path := generateMutatePath(testValidatorGVK)
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusNotFound))

		By("sending a request to a validating webhook path")
		path = generateValidatePath(testValidatorGVK)
		_, err = reader.Seek(0, 0)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req = httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w = httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable field")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":false`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":403`))
		EventuallyWithOffset(1, logBuffer).Should(gbytes.Say(`"msg":"Validating object","object":{"name":"foo","namespace":"default"},"namespace":"default","name":"foo","resource":{"group":"foo.test.org","version":"v1","resource":"testvalidator"},"user":"","requestID":"07e52e8d-4513-11e9-a716-42010a800270"`))
	})

	It("should scaffold a custom validating webhook with a custom path", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testValidatorGVK.GroupVersion()}
		builder.Register(&TestValidator{}, &TestValidatorList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		customPath := "/custom-validating-path"
		err = WebhookManagedBy(m).
			For(&TestValidator{}).
			WithValidator(&TestCustomValidator{}).
			WithLogConstructor(func(base logr.Logger, req *admission.Request) logr.Logger {
				return admission.DefaultLogConstructor(testingLogger, req)
			}).
			WithCustomPath(customPath).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"foo.test.org",
      "version":"v1",
      "kind":"TestValidator"
    },
    "resource":{
      "group":"foo.test.org",
      "version":"v1",
      "resource":"testvalidator"
    },
    "namespace":"default",
    "name":"foo",
    "operation":"UPDATE",
    "object":{
      "replica":1
    },
    "oldObject":{
      "replica":2
    }
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a mutating webhook path that have been overrided by a custom path")
		path := generateValidatePath(testValidatorGVK)
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusNotFound))

		By("sending a request to a validating webhook path")
		path, err = generateCustomPath(customPath)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		_, err = reader.Seek(0, 0)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req = httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w = httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable field")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":false`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":403`))
		EventuallyWithOffset(1, logBuffer).Should(gbytes.Say(`"msg":"Validating object","object":{"name":"foo","namespace":"default"},"namespace":"default","name":"foo","resource":{"group":"foo.test.org","version":"v1","resource":"testvalidator"},"user":"","requestID":"07e52e8d-4513-11e9-a716-42010a800270"`))
	})

	It("should scaffold a custom validating webhook which recovers from panics", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testValidatorGVK.GroupVersion()}
		builder.Register(&TestValidator{}, &TestValidatorList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestValidator{}).
			WithValidator(&TestCustomValidator{}).
			RecoverPanic(true).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"",
      "version":"v1",
      "kind":"TestValidator"
    },
    "resource":{
      "group":"",
      "version":"v1",
      "resource":"testvalidator"
    },
    "namespace":"default",
    "operation":"CREATE",
    "object":{
      "replica":2,
      "panic":true
    }
  }
}`)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a validating webhook path")
		path := generateValidatePath(testValidatorGVK)
		_, err = reader.Seek(0, 0)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable field")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":false`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":500`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"message":"panic: fake panic test [recovered]`))
	})

	It("should scaffold a custom validating webhook to validate deletes", func() {
		By("creating a controller manager")
		ctx, cancel := context.WithCancel(context.Background())

		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testValidatorGVK.GroupVersion()}
		builder.Register(&TestValidator{}, &TestValidatorList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestValidator{}).
			WithValidator(&TestCustomValidator{}).
			Complete()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		svr := m.GetWebhookServer()
		ExpectWithOffset(1, svr).NotTo(BeNil())

		reader := strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"",
      "version":"v1",
      "kind":"TestValidator"
    },
    "resource":{
      "group":"",
      "version":"v1",
      "resource":"testvalidator"
    },
    "namespace":"default",
    "operation":"DELETE",
    "object":null,
    "oldObject":{
      "replica":1
    }
  }
}`)

		cancel()
		err = svr.Start(ctx)
		if err != nil && !os.IsNotExist(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}

		By("sending a request to a validating webhook path to check for failed delete")
		path := generateValidatePath(testValidatorGVK)
		req := httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable field")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":false`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":403`))

		reader = strings.NewReader(admissionReviewGV + admissionReviewVersion + `",
  "request":{
    "uid":"07e52e8d-4513-11e9-a716-42010a800270",
    "kind":{
      "group":"",
      "version":"v1",
      "kind":"TestValidator"
    },
    "resource":{
      "group":"",
      "version":"v1",
      "resource":"testvalidator"
    },
    "namespace":"default",
    "operation":"DELETE",
    "object":null,
    "oldObject":{
      "replica":0
    }
  }
}`)
		By("sending a request to a validating webhook path with correct request")
		path = generateValidatePath(testValidatorGVK)
		req = httptest.NewRequest("POST", svcBaseAddr+path, reader)
		req.Header.Add("Content-Type", "application/json")
		w = httptest.NewRecorder()
		svr.WebhookMux().ServeHTTP(w, req)
		ExpectWithOffset(1, w.Code).To(Equal(http.StatusOK))
		By("sanity checking the response contains reasonable field")
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"allowed":true`))
		ExpectWithOffset(1, w.Body).To(ContainSubstring(`"code":200`))
	})

	It("should send an error when trying to register a webhook with more than one For", func() {
		By("creating a controller manager")
		m, err := manager.New(cfg, manager.Options{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("registering the type in the Scheme")
		builder := scheme.Builder{GroupVersion: testDefaulterGVK.GroupVersion()}
		builder.Register(&TestDefaulter{}, &TestDefaulterList{})
		err = builder.AddToScheme(m.GetScheme())
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		err = WebhookManagedBy(m).
			For(&TestDefaulter{}).
			For(&TestDefaulter{}).
			Complete()
		Expect(err).To(HaveOccurred())
	})
}

// TestDefaulter.
var _ runtime.Object = &TestDefaulter{}

const testDefaulterKind = "TestDefaulter"

type TestDefaulter struct {
	Replica int  `json:"replica,omitempty"`
	Panic   bool `json:"panic,omitempty"`
}

var testDefaulterGVK = schema.GroupVersionKind{Group: "foo.test.org", Version: "v1", Kind: testDefaulterKind}

func (d *TestDefaulter) GetObjectKind() schema.ObjectKind { return d }
func (d *TestDefaulter) DeepCopyObject() runtime.Object {
	return &TestDefaulter{
		Replica: d.Replica,
	}
}

func (d *TestDefaulter) GroupVersionKind() schema.GroupVersionKind {
	return testDefaulterGVK
}

func (d *TestDefaulter) SetGroupVersionKind(gvk schema.GroupVersionKind) {}

var _ runtime.Object = &TestDefaulterList{}

type TestDefaulterList struct{}

func (*TestDefaulterList) GetObjectKind() schema.ObjectKind { return nil }
func (*TestDefaulterList) DeepCopyObject() runtime.Object   { return nil }

// TestValidator.
var _ runtime.Object = &TestValidator{}

const testValidatorKind = "TestValidator"

type TestValidator struct {
	Replica int  `json:"replica,omitempty"`
	Panic   bool `json:"panic,omitempty"`
}

var testValidatorGVK = schema.GroupVersionKind{Group: "foo.test.org", Version: "v1", Kind: testValidatorKind}

func (v *TestValidator) GetObjectKind() schema.ObjectKind { return v }
func (v *TestValidator) DeepCopyObject() runtime.Object {
	return &TestValidator{
		Replica: v.Replica,
	}
}

func (v *TestValidator) GroupVersionKind() schema.GroupVersionKind {
	return testValidatorGVK
}

func (v *TestValidator) SetGroupVersionKind(gvk schema.GroupVersionKind) {}

var _ runtime.Object = &TestValidatorList{}

type TestValidatorList struct{}

func (*TestValidatorList) GetObjectKind() schema.ObjectKind { return nil }
func (*TestValidatorList) DeepCopyObject() runtime.Object   { return nil }

// TestDefaultValidator.
var _ runtime.Object = &TestDefaultValidator{}

type TestDefaultValidator struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Replica int `json:"replica,omitempty"`
}

var testDefaultValidatorGVK = schema.GroupVersionKind{Group: "foo.test.org", Version: "v1", Kind: "TestDefaultValidator"}

func (dv *TestDefaultValidator) GetObjectKind() schema.ObjectKind { return dv }
func (dv *TestDefaultValidator) DeepCopyObject() runtime.Object {
	return &TestDefaultValidator{
		Replica: dv.Replica,
	}
}

func (dv *TestDefaultValidator) GroupVersionKind() schema.GroupVersionKind {
	return testDefaultValidatorGVK
}

func (dv *TestDefaultValidator) SetGroupVersionKind(gvk schema.GroupVersionKind) {}

var _ runtime.Object = &TestDefaultValidatorList{}

type TestDefaultValidatorList struct{}

func (*TestDefaultValidatorList) GetObjectKind() schema.ObjectKind { return nil }
func (*TestDefaultValidatorList) DeepCopyObject() runtime.Object   { return nil }

// TestCustomDefaulter.
type TestCustomDefaulter struct{}

func (*TestCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	logf.FromContext(ctx).Info("Defaulting object")
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("expected admission.Request in ctx: %w", err)
	}
	if req.Kind.Kind != testDefaulterKind {
		return fmt.Errorf("expected Kind TestDefaulter got %q", req.Kind.Kind)
	}

	d := obj.(*TestDefaulter) //nolint:ifshort
	if d.Panic {
		panic("fake panic test")
	}

	if d.Replica < 2 {
		d.Replica = 2
	}
	return nil
}

var _ admission.CustomDefaulter = &TestCustomDefaulter{}

// TestCustomValidator.

type TestCustomValidator struct{}

func (*TestCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validating object")
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("expected admission.Request in ctx: %w", err)
	}
	if req.Kind.Kind != testValidatorKind {
		return nil, fmt.Errorf("expected Kind TestValidator got %q", req.Kind.Kind)
	}

	v := obj.(*TestValidator) //nolint:ifshort
	if v.Panic {
		panic("fake panic test")
	}
	if v.Replica < 0 {
		return nil, errors.New("number of replica should be greater than or equal to 0")
	}
	return nil, nil
}

func (*TestCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validating object")
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("expected admission.Request in ctx: %w", err)
	}
	if req.Kind.Kind != testValidatorKind {
		return nil, fmt.Errorf("expected Kind TestValidator got %q", req.Kind.Kind)
	}

	v := newObj.(*TestValidator)
	old := oldObj.(*TestValidator)
	if v.Replica < 0 {
		return nil, errors.New("number of replica should be greater than or equal to 0")
	}
	if v.Replica < old.Replica {
		return nil, fmt.Errorf("new replica %v should not be fewer than old replica %v", v.Replica, old.Replica)
	}
	return nil, nil
}

func (*TestCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validating object")
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("expected admission.Request in ctx: %w", err)
	}
	if req.Kind.Kind != testValidatorKind {
		return nil, fmt.Errorf("expected Kind TestValidator got %q", req.Kind.Kind)
	}

	v := obj.(*TestValidator) //nolint:ifshort
	if v.Replica > 0 {
		return nil, errors.New("number of replica should be less than or equal to 0 to delete")
	}
	return nil, nil
}

var _ admission.CustomValidator = &TestCustomValidator{}
